#!/bin/bash

GRAPHITE_PORT=2003
GRAPHITE_SERVER="52.41.234.112"
usage() {
    echo "Usage: $0 -a <aws.env> [-s]"
    echo "   -a : aws.env - AWS credentials"
    echo "   -g : name or ip address of graphite server (default is $GRAPHITE_SERVER)"
    echo "   -p : port of graphite server (default is $GRAPHITE_PORT)"
    echo
    exit 1;
}
environment="k8s";
aws_env="";
os="ubuntu"
while getopts "a:g:p:" OPTION
do
    case $OPTION in
    a)
        aws_env=${OPTARG}
        ;;

    g)
        GRAPHITE_SERVER=${OPTARG}
        ;;

    p)
        GRAPHITE_PORT=${OPTARG}
        ;;

    *)
        usage
        ;;
    esac
done

DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
if [ "${PWD##*/}" != "docker" ]; then
    cd $DIR
fi

if [ -z "$aws_env" ]; then
   echo -a is a required parameter.Provide the path for aws.env file
   usage
fi

PROXYURL="http://aisproxy:8080"
PROXYID="ORIGINAL_PRIMARY"
PORT=8080
SERVICENAME="ais"
LOGDIR="/tmp/ais/log"
LOGLEVEL="3"
CONFDIR="/usr/nvidia"
###################################
#
# fspaths config is used if and only if test_fspaths.count == 0
# existence of each fspath is checked at runtime
#
###################################
TESTFSPATHROOT="/tmp/ais/"

echo Enter number of cache servers:
read servcount
if ! [[ "$servcount" =~ ^[0-9]+$ ]] ; then
  echo "Error: '$servcount' is not a number"; exit 1
fi
echo Enter number of proxy servers:
read proxycount
if ! [[ "$proxycount" =~ ^[0-9]+$ ]] ; then
  echo "Error: '$proxycount' must be at least 1"; exit 1
elif [ $proxycount -lt 1 ] ; then
  echo "Error: $proxycount is less than 1"; exit 1
fi
START=0
END=$servcount


testfspathcnt=0
fspath="\"\":\"\""
echo Select
echo  1: Local cache directories
echo  2: Filesystems
echo Enter your cache choice:
read cachesource
if [ $cachesource -eq 1 ]
then
   echo Enter number of local cache directories:
   read testfspathcnt
   if ! [[ "$testfspathcnt" =~ ^[0-9]+$ ]] ; then
       echo "Error: '$testfspathcnt' is not a number"; exit 1
   fi
fi
if [ $cachesource -eq 2 ]
then
   echo Enter filesystem info in comma separated format ex: /tmp/ais1,/tmp/ais:
   read fsinfo
   fspath=""
   IFS=',' read -r -a array <<< "$fsinfo"
   for element in "${array[@]}"
   do
      fspath="$fspath,\"$element\" : \"\" "
   done
   fspath=${fspath#","}
fi

echo $FSPATHS
FSPATHS=$fspath
TESTFSPATHCOUNT=$testfspathcnt

CLDPROVIDER="" # See deploy.sh for more informations about empty CLDPROVIDER
echo Select Cloud Provider:
echo  1: Amazon Cloud
echo  2: Google Cloud
echo  3: None
echo Enter your choice:
read cldprovider
if [ $cldprovider -eq 1 ]; then
    CLDPROVIDER="aws"
    cp $aws_env .
    # creating aws credential files
    rm -rf credentials
    cat $aws_env >> credentials
    sed -i '1 i\[default]' credentials
    sed -i 's/AWS_ACCESS_KEY_ID/aws_access_key_id/g' credentials
    sed -i 's/AWS_SECRET_ACCESS_KEY/aws_secret_access_key/g' credentials
    sed -i 's/AWS_DEFAULT_REGION/region/g' credentials
    kubectl delete secret generic aws-credentials
    kubectl create secret generic aws-credentials --from-file=./credentials
elif [ $cldprovider -eq 2 ]
then
  CLDPROVIDER="gcp"
fi

CONFFILE="ais.json"
c=0
CONFFILE_STATSD="statsd.conf"
CONFFILE_COLLECTD="collectd.conf"
source $DIR/../ais/setup/config.sh

#1) create/update/delete kubctl configmap
#)  run the cluster

# Deploying kubernetes cluster
echo Starting kubernetes deployment ..
#Create AIStore configmap to attach during runtime
echo Creating AIStore configMap
kubectl delete configmap ais-config
kubectl delete configmap collectd-config
kubectl delete configmap statsd-config
kubectl create configmap ais-config --from-file=ais.json
kubectl create configmap statsd-config --from-file=statsd.conf
kubectl create configmap collectd-config --from-file=collectd.conf

echo Stopping AIStore cluster
kubectl delete -f aistarget_deployment.yml
kubectl delete -f aisproxy_deployment.yml
kubectl delete -f aisprimaryproxy_deployment.yml

echo Starting Primary Proxy Deployment
kubectl create -f aisprimaryproxy_deployment.yml

echo Wating for proxy to start ....
sleep 100

echo Starting Proxy Deployment
kubectl create -f aisproxy_deployment.yml

echo Scaling proxies
kubectl scale --replicas=$proxycount -f aisproxy_deployment.yml

echo Starting Target Deployment
kubectl create -f aistarget_deployment.yml

echo Scaling targets
kubectl scale --replicas=$servcount -f aistarget_deployment.yml

echo List of running pods
kubectl get pods -o wide
echo done
