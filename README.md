Introduction
===============

Watches Kubernetes API server for Pods & Services (de)registration. 

Route rule:

		http://[pods].[namespace].k8s:8000/ --> http://[pods-cluster-ip]:[first-port]
		http://[service].[namespace].k8s:8000/ --> http://[service-cluster-ip]:[first-port]

Port must be TCP protocol and not 443 / 8443.

Prerequisite [Mac OSX]
===============
* Install boot2docker
* Launch boot2docker
* Prepare GoLang directory

		export GOPATH=~/Code/Go

* Create compiler alias

		alias go="docker run --rm -v "$GOPATH":/go -w /go golang go"

Prerequisite [Linux]
===============
* Install docker
* Install GoLang compiler
* Prepare GoLang directory

		export GOPATH=~/Code/Go

Build dependency packages
===============
* WebSocket

		go get github.com/gorilla/websocket

* EtcD

		go get github.com/coreos/go-etcd/etcd

* K8s API

		go get github.com/GoogleCloudPlatform/kubernetes/pkg/api

Build binary
===============
* Download and build 

		go get github.com/rainbean/Kube2Vulcan 

* Build only

		go build github.com/rainbean/Kube2Vulcan

Usage Syntax
===============
		Kube2Vulcan -master [k8s-master-ip]:[port] -etcd http://[etcd-ip]:[port],http://[2nd-etcd-ip]:[port],...

Running as Contrainner
===============
		docker run -d quay.io/rainbean/kube2vulcan:latest -master 192.168.200.60:8080 -etcd http://127.0.0.1:2379

Running as k8s service
===============
		kubectl create -f kube2vulcan.yaml


