Introduction
===============

Watches Kubernetes API server for Pods & Services (de)registration. 

Route rule:

		http://[pod-name or service-name].[namespace].your-domain:[exposed-port]/ --> http://[pods-ip or service-cluster-ip]:[exposed-port]

Port must be TCP protocol

Prerequisite [Mac OSX]
===============
* Install boot2docker
* Launch boot2docker
* Prepare GoLang directory

		export GOPATH=~/Code/Go

* Create compiler alias

		alias goc="docker run --rm -v "$GOPATH":/go -w /go -e CGO_ENABLED=0 -e GOOS=linux golang go"

Prerequisite [Linux]
===============
* Install docker
* Install GoLang compiler
* Prepare GoLang directory

		export GOPATH=~/Code/Go

* Create compiler alias

		alias goc="CGO_ENABLED=0 GOOS=linux go"

Build dependency packages
===============
* WebSocket

		goc get github.com/gorilla/websocket

* Etcd Client API

		goc github.com/coreos/etcd/client

* K8s API

		goc get github.com/GoogleCloudPlatform/kubernetes/pkg/api

Build binary
===============
* Download and build 

		goc get -a -installsuffix cgo github.com/rainbean/Kube2Vulcan 

* Build only

		goc build -a -installsuffix cgo github.com/rainbean/Kube2Vulcan

Note
===============
Environment and build arugment is to statically compile our app with all libraries built in, refer https://blog.codeship.com/building-minimal-docker-containers-for-go-applications/

		CGO_ENABLED=0 GOOS=linux 
		build -a -installsuffix cgo 


Usage Syntax
===============
First ports must be Vulcand's listen port, addtional ports cover exposed service and pods

		Kube2Vulcan -master [k8s-master-ip]:[port] -etcd http://[etcd-ip]:[port],http://[2nd-etcd-ip]:[port],..., -ports [vulcand-port][,addtional ports]

Running as Contrainner
===============
		docker run -d quay.io/rainbean/kube2vulcan:latest -master 192.168.200.60:8080 -etcd http://127.0.0.1:2379 -ports 8000

Running as k8s service
===============
		kubectl create -f kube2vulcan.yaml


