FROM golang:1.13-alpine AS builder

ENV CGO_ENABLED=0
ENV GO111MODULE=off

#RUN echo "https://mirrors.aliyun.com/alpine/v3.11/main/" > /etc/apk/repositories
#RUN echo "https://mirrors.aliyun.com/alpine/v3.11/community/" >> /etc/apk/repositories
RUN sed -i 's#://dl-cdn.alpinelinux.org#s://mirrors.aliyun.com#g' /etc/apk/repositories

RUN apk add --no-cache git g++ linux-headers curl ca-certificates

# RUN curl -sSLf https://amazon-ecr-credential-helper-releases.s3.us-east-2.amazonaws.com/0.3.1/linux-amd64/docker-credential-ecr-login > docker-credential-ecr-login \
#  && chmod +x docker-credential-ecr-login \
#  && mv docker-credential-ecr-login /usr/local/bin/

WORKDIR /go/src/github.com/openfaas/openfaas-cloud/of-builder

ADD main.go     .
ADD healthz.go  .
ADD vendor      vendor

RUN go build -o /usr/bin/of-builder .

FROM alpine:3.12

#RUN echo "https://mirrors.aliyun.com/alpine/v3.12/main/" > /etc/apk/repositories
#RUN echo "https://mirrors.aliyun.com/alpine/v3.12/community/" >> /etc/apk/repositories
RUN sed -i 's#://dl-cdn.alpinelinux.org#s://mirrors.aliyun.com#g' /etc/apk/repositories

RUN apk add --no-cache ca-certificates

# Setting the group prevented access to /tmp at runtime
# lchown started to fail
# -G app 
RUN addgroup -S app && adduser app -S \
 && mkdir -p /home/app && mkdir -p /usr/share/openfaas/template

#RUN mkdir -p /home/app/.aws/

WORKDIR /home/app

# COPY --from=builder /usr/local/bin/docker-credential-ecr-login  /usr/local/bin/
COPY --from=builder /usr/bin/of-builder /home/app/
COPY faas-cli/faas-cli /usr/bin/
COPY faas-cli/create.sh /usr/bin/
RUN chmod +x /usr/bin/faas-cli /usr/bin/create.sh
COPY faas-cli/template /usr/share/openfaas/template/

RUN chown -R app /home/app
#USER app

EXPOSE 8080
VOLUME /tmp/

ENTRYPOINT ["/home/app/of-builder"]
