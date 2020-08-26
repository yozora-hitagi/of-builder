package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alexellis/hmac"
	"github.com/docker/docker/pkg/archive"
	"github.com/gorilla/mux"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/openfaas/openfaas-cloud/sdk"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

// ConfigFileName for Docker bundle
const ConfigFileName = "com.openfaas.docker.config"

// DefaultFrontEnd to run the build with buildkit
const DefaultFrontEnd = "tonistiigi/dockerfile:v0"

var (
	lchownEnabled bool
	buildkitURL   string
	buildArgs     = map[string]string{}
)

type buildConfig struct {
	Ref       string            `json:"ref"`
	Frontend  string            `json:"frontend,omitempty"`
	BuildArgs map[string]string `json:"buildArgs,omitempty"`
}

func main() {
	flag.Parse()

	lchownEnabled = true
	if val, exists := os.LookupEnv("enable_lchown"); exists {
		if val == "false" {
			lchownEnabled = false
		}
	}

	buildkitURL = "tcp://of-buildkit:1234"
	if val, ok := os.LookupEnv("buildkit_url"); ok && len(val) > 0 {
		buildkitURL = val
	}

	if val, ok := os.LookupEnv("http_proxy"); ok {
		buildArgs["build-arg:http_proxy"] = val
	}

	if val, ok := os.LookupEnv("https_proxy"); ok {
		buildArgs["build-arg:https_proxy"] = val
	}

	if val, ok := os.LookupEnv("no_proxy"); ok {
		buildArgs["build-arg:no_proxy"] = val
	}

	router := mux.NewRouter().StrictSlash(true)
	router.HandleFunc("/build", buildHandler)
	router.HandleFunc("/healthz", healthzHandler)

	router.HandleFunc("/create", createHandler)

	addr := "0.0.0.0:8080"
	log.Printf("of-builder serving traffic on: %s\n", addr)

	server := &http.Server{
		Addr:    addr,
		Handler: router,
	}

	eg, ctx := errgroup.WithContext(appcontext.Context())

	eg.Go(func() error {
		<-ctx.Done()
		return server.Shutdown(context.Background())
	})

	eg.Go(func() error {
		return server.ListenAndServe()
	})

	if err := eg.Wait(); err != nil {
		panic(err)
	}
}

func buildHandler(w http.ResponseWriter, r *http.Request) {

	dt, err := build(w, r, buildArgs)

	if err != nil {
		w.WriteHeader(500)

		if dt == nil {
			buildResult := BuildResult{
				ImageName: "",
				Log:       nil,
				Status:    fmt.Sprintf("unexpected failure: %s", err.Error()),
			}
			dt, _ = json.Marshal(buildResult)
		}
		w.Write(dt)

		// w.Write([]byte(fmt.Sprintf("%s", err.Error())))
		return
	}
	w.WriteHeader(200)
	w.Write(dt)
}

func build(w http.ResponseWriter, r *http.Request, buildArgs map[string]string) ([]byte, error) {

	if r.Body == nil {
		return nil, fmt.Errorf("a body is required to build a function")
	}

	defer r.Body.Close()

	tmpdir, err := ioutil.TempDir("", "buildctx")
	if err != nil {
		return nil, fmt.Errorf("fail 1: %s", err)
	}

	tarBytes, bodyErr := ioutil.ReadAll(r.Body)
	if bodyErr != nil {
		return nil, fmt.Errorf("fail 2: %s", bodyErr)
	}

	enforceHMAC := true
	if val, ok := os.LookupEnv("disable_hmac"); ok && val == "true" {
		enforceHMAC = false
	}

	if enforceHMAC {
		hmacErr := validateRequest(&tarBytes, r)
		if hmacErr != nil {
			return nil, fmt.Errorf("fail 3: %s", hmacErr)
		}
	}

	defer os.RemoveAll(tmpdir)

	opts := archive.TarOptions{
		NoLchown: !lchownEnabled,
	}

	if err := archive.Untar(bytes.NewReader(tarBytes), tmpdir, &opts); err != nil {
		return nil, fmt.Errorf("untar: %s", err)
	}

	dt, err := ioutil.ReadFile(filepath.Join(tmpdir, ConfigFileName))
	if err != nil {
		return nil, fmt.Errorf("fail 4: %s", err)
	}

	cfg := buildConfig{}
	if err := json.Unmarshal(dt, &cfg); err != nil {
		return nil, err
	}

	if cfg.Ref == "" {
		return nil, errors.Errorf("no target reference to push")
	}

	if cfg.Frontend == "" {
		cfg.Frontend = DefaultFrontEnd
	}

	insecure := "false"
	if val, exists := os.LookupEnv("insecure"); exists {
		insecure = val
	}

	frontendAttrs := map[string]string{
		"source": cfg.Frontend,
	}

	for k, v := range buildArgs {
		frontendAttrs[k] = v
	}

	for k, v := range cfg.BuildArgs {
		frontendAttrs[fmt.Sprintf("build-arg:%s", k)] = v
	}

	contextDir := filepath.Join(tmpdir, "context")
	solveOpt := client.SolveOpt{

		Exports: []client.ExportEntry{
			{
				Type: "image",
				Attrs: map[string]string{
					"name": strings.ToLower(cfg.Ref),
					"push": "true",
				},
			},
		},
		LocalDirs: map[string]string{
			"context":    contextDir,
			"dockerfile": contextDir,
		},
		Frontend:      "dockerfile.v0",
		FrontendAttrs: frontendAttrs,
		// ~/.docker/config.json could be provided as Kube or Swarm's secret
		Session: []session.Attachable{authprovider.NewDockerAuthProvider(ioutil.Discard)},
	}

	if insecure == "true" {
		solveOpt.Exports[0].Attrs["registry.insecure"] = insecure
	}

	c, err := client.New(context.Background(), buildkitURL, client.WithFailFast())
	if err != nil {
		return nil, fmt.Errorf("fail 5: %s", err)
	}

	ch := make(chan *client.SolveStatus)
	eg, ctx := errgroup.WithContext(context.Background())
	eg.Go(func() error {
		_, err := c.Solve(ctx, nil, solveOpt, ch)
		return err
	})

	build := buildLog{
		Line: []string{},
		Sync: &sync.Mutex{},
	}

	eg.Go(func() error {
		for s := range ch {
			for _, v := range s.Vertexes {
				var msg string
				if v.Completed != nil {
					msg = fmt.Sprintf("v: %s %s %.2fs", v.Started.Format(time.RFC3339), v.Name, v.Completed.Sub(*v.Started).Seconds())
				} else {
					var startedTime time.Time
					if v.Started != nil {
						startedTime = *(v.Started)
					} else {
						startedTime = time.Now()
					}
					startedVal := startedTime.Format(time.RFC3339)
					msg = fmt.Sprintf("v: %s %v", startedVal, v.Name)
				}
				build.Append(msg)
				fmt.Printf("%s\n", msg)

			}
			for _, s := range s.Statuses {
				msg := fmt.Sprintf("s: %s %s %d", s.Timestamp.Format(time.RFC3339), s.ID, s.Current)
				build.Append(msg)

				fmt.Printf("status: %s %s %d\n", s.Vertex, s.ID, s.Current)
			}
			for _, l := range s.Logs {

				msg := fmt.Sprintf("l: %s %s", l.Timestamp.Format(time.RFC3339), l.Data)
				build.Append(msg)

				fmt.Printf("log: %s\n%s\n", l.Vertex, l.Data)
			}

		}
		return nil
	})

	if err := eg.Wait(); err != nil {

		buildResult := BuildResult{
			ImageName: cfg.Ref,
			Log:       build.Line,
			Status:    fmt.Sprintf("failure: %s", err.Error()),
		}

		bytesOut, _ := json.Marshal(buildResult)
		return bytesOut, err
	}

	buildResult := BuildResult{
		ImageName: cfg.Ref,
		Log:       build.Line,
		Status:    "success",
	}

	bytesOut, _ := json.Marshal(buildResult)

	return bytesOut, nil
}

// BuildResult represents a successful Docker build and
// push operation to a remote registry
type BuildResult struct {
	Log       []string `json:"log"`
	ImageName string   `json:"imageName"`
	Status    string   `json:"status"`
}

type buildLog struct {
	Line []string
	Sync *sync.Mutex
}

func (b *buildLog) Append(msg string) {
	b.Sync.Lock()
	defer b.Sync.Unlock()

	b.Line = append(b.Line, msg)

}

func validateRequest(req *[]byte, r *http.Request) (err error) {
	payloadSecret, err := sdk.ReadSecret("payload-secret")

	if err != nil {
		return fmt.Errorf("couldn't get payload-secret: %t", err)
	}

	xCloudSignature := r.Header.Get(sdk.CloudSignatureHeader)

	err = hmac.Validate(*req, xCloudSignature, payloadSecret)

	if err != nil {
		return err
	}

	return nil
}

func createHandler(w http.ResponseWriter, r *http.Request) {

	err := create(w, r)

	if err != nil {
		w.WriteHeader(500)

		buildResult := BuildResult{
			ImageName: "",
			Log:       nil,
			Status:    fmt.Sprintf("create function error : %s", err.Error()),
		}
		dt, _ := json.Marshal(buildResult)
		w.Write(dt)

		// w.Write([]byte(fmt.Sprintf("%s", err.Error())))
	}

}

type CreateArgs struct {
	Registry   string `json:"registry"` //"fitregistry.fiberhome.com/openfaas-fn"
	Fn_name    string `json:"name"`
	Fn_lang    string `json:"lang"`
	Fn_version string `json:"version"`
}

func create(w http.ResponseWriter, r *http.Request) error {

	if r.Body == nil {
		return fmt.Errorf("a body is required to build a function")
	}

	defer r.Body.Close()

	body, err := ioutil.ReadAll(r.Body)

	if err != nil {
		return fmt.Errorf("read request body error : %s", err)
	}

	args := CreateArgs{}

	err = json.Unmarshal(body, &args)
	if err != nil {
		return fmt.Errorf("json.Unmarshal : %s", err)
	}

	tmpdir, err := ioutil.TempDir("", "createctx")
	if err != nil {
		return fmt.Errorf("fail 1: %s", err)
	}

	defer os.RemoveAll(tmpdir)

	cmd := exec.Command("create.sh")

	env := os.Environ()
	cmdEnv := []string{"FN_TMP_DIR=" + tmpdir, "REGISTRY=" + args.Registry, "FN=" + args.Fn_name, "FN_LANG=" + args.Fn_lang, "FN_VER=" + args.Fn_version}

	for _, e := range env {
		i := strings.Index(e, "=")
		if i > 0 && (e[:i] == "FN_TMP_DIR" || e[:i] == "REGISTRY" || e[:i] == "FN" || e[:i] == "FN_LANG" || e[:i] == "FN_VER") {
			// do yourself
		} else {
			cmdEnv = append(cmdEnv, e)
		}
	}
	cmd.Env = cmdEnv

	_, err = cmd.CombinedOutput()

	if err != nil {
		return fmt.Errorf("exec.Command : %s", err)
	}

	dt, err := ioutil.ReadFile(filepath.Join(tmpdir, args.Fn_name+"-context.tar"))

	w.WriteHeader(200)
	w.Header().Add("Content-Disposition", "attachment;Filename="+args.Fn_name+"-context.tar")
	w.Header().Add("Content-type", "application/octet-stream")
	w.Write(dt)

	return err
}

//func Command(name string, arg ...string) *Cmd
//方法返回一个*Cmd， 用于执行name指定的程序(携带arg参数)
//func (c *Cmd) Run() error
//执行Cmd中包含的命令，阻塞直到命令执行完成
//func (c *Cmd) Start() error
//执行Cmd中包含的命令，该方法立即返回，并不等待命令执行完成
//func (c *Cmd) Wait() error
//该方法会阻塞直到Cmd中的命令执行完成，但该命令必须是被Start方法开始执行的
//func (c *Cmd) Output() ([]byte, error)
//执行Cmd中包含的命令，并返回标准输出的切片
//func (c *Cmd) CombinedOutput() ([]byte, error)
//执行Cmd中包含的命令，并返回标准输出与标准错误合并后的切片
//func (c *Cmd) StdinPipe() (io.WriteCloser, error)
//返回一个管道，该管道会在Cmd中的命令被启动后连接到其标准输入
//func (c *Cmd) StdoutPipe() (io.ReadCloser, error)
//返回一个管道，该管道会在Cmd中的命令被启动后连接到其标准输出
//func (c *Cmd) StderrPipe() (io.ReadCloser, error)
//返回一个管道，该管道会在Cmd中的命令被启动后连接到其标准错误
