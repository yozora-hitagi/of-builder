#!/bin/bash
echo "构建"
make build
echo "修改TAG"
docker tag openfaas/of-builder fitregistry.fiberhome.com/openfaas/of-builder
echo "推送"
docker push fitregistry.fiberhome.com/openfaas/of-builder


