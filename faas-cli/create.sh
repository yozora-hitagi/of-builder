#!/bin/sh


#export REGISTRY="fitregistry.fiberhome.com/openfaas-fn"
#export FN="hello-go"
#export FN_LANG="go"
#export FN_VER="latest"
#export FN_TMP_DIR="tt"


export TEMPLATE_DIR=/usr/share/openfaas/template


mkdir -p $FN_TMP_DIR
cd $FN_TMP_DIR

cp -R $TEMPLATE_DIR .

faas-cli new --lang $FN_LANG $FN
faas-cli build --shrinkwrap -f $FN.yml


rm -fr tmp
mkdir -p tmp/context

echo '{"Ref": "'${REGISTRY}'/'${FN}':'${FN_VER}'"}' > tmp/com.openfaas.docker.config

cp -r build/$FN/* tmp/context
tar -C ./tmp -cvf $FN-context.tar .
