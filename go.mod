module github.com/openfaas/openfaas-cloud/of-builder-dev

go 1.13

replace github.com/hashicorp/go-immutable-radix => github.com/tonistiigi/go-immutable-radix v0.0.0-20170803185627-826af9ccf0fe

replace github.com/jaguilar/vt100 => github.com/tonistiigi/vt100 v0.0.0-20190402012908-ad4c4a574305

replace github.com/containerd/containerd => github.com/containerd/containerd v1.3.1-0.20200227195959-4d242818bf55

replace github.com/docker/docker => github.com/docker/docker v1.4.2-0.20200227233006-38f52c9fec82

require (
	github.com/alexellis/hmac v0.0.0-20180624211220-5c52ab81c0de
	github.com/docker/docker v0.0.0
	github.com/gorilla/mux v1.7.4
	github.com/moby/buildkit v0.7.2
	github.com/openfaas/faas-provider v0.15.1 // indirect
	github.com/openfaas/openfaas-cloud v0.0.0-20200805081416-6f2020582268
	github.com/pkg/errors v0.9.1
	golang.org/x/sync v0.0.0-20200625203802-6e8e738ad208
)
