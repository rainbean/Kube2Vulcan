/*****************************************************************************
* (C) Copyright 2013-2014 Compal Electronics, Inc.
*
* This software is the property of Compal Electronics, Inc.
* You have to accept the terms in the license file before use.
*****************************************************************************/
package main

import (
	"github.com/gorilla/websocket"
	"net"
	"net/http"
	"net/url"
	"log"
	"encoding/json"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"flag"
	"github.com/coreos/etcd/client"
	"golang.org/x/net/context"
	"fmt"
	"strings"
	"time"
	"strconv"
)

type Endpoint struct {
  Name string
  Namespace string
  IP string
  Port int
}

var (
	argK8sAddr              = flag.String("master", "", "Address of Kubernetes API server")
	argEtcdAddr             = flag.String("etcd", "", "Address of etcd of Vulcan daemon")
	argVulcandPorts         = flag.String("ports", "8000", "Valid ports to proxy")
	argRetainHostHeader     = flag.Bool("retainHostHeader", false, "Retain pristin client host header, default:false")
	kapi client.KeysAPI 
)

// Main function
// Connect to Kubernetes API server and monitor for PODS/SVC events, 
// then sync to etcd in syntax of vulcand
func main() {
	flag.Parse()
	
	if *argK8sAddr == "" || *argEtcdAddr == "" {
		log.Fatal(`Missing required properties. Usage: -master "[k8s-master-ip]:[port]" -etcd "http://[etcd-ip]:[port],http://[2nd-etcd-ip]:[port],..." -ports "8000,8080"`)
	}
	
	// create etcd client connection
	cfg := client.Config{
        Endpoints:               []string{*argEtcdAddr},
        Transport:               client.DefaultTransport,
        // set timeout per request to fail fast when the target endpoint is unavailable
        HeaderTimeoutPerRequest: time.Second,
    }
    etcClient, err := client.New(cfg)
    if err != nil {
        log.Fatal(err)
    }

	// get etcd key api
	kapi = client.NewKeysAPI(etcClient)
	addListenPorts()

	var wsPodsErrors chan string = make(chan string)
	var wsSvcErrors chan string = make(chan string)

	podsEndpoint := fmt.Sprintf("ws://%v/api/v1/pods?watch=true", *argK8sAddr)
	svcEndpoint := fmt.Sprintf("ws://%v/api/v1/services?watch=true", *argK8sAddr)
	
	go podsListener(openConnection(podsEndpoint), wsPodsErrors)
	go svcListener(openConnection(svcEndpoint), wsSvcErrors)
		
	for {
		// uncondtional blocking until either case occured
		select {
			case <- wsPodsErrors:
				log.Println("Reconnecting...")
				time.Sleep(5 * time.Second) // sleep few second(s) before retry connection
				go podsListener(openConnection(podsEndpoint), wsPodsErrors)
			case <- wsSvcErrors:
				log.Println("Reconnecting...")
				time.Sleep(5 * time.Second) // sleep few second(s) before retry connection
				go svcListener(openConnection(svcEndpoint), wsSvcErrors)
		}
	}
}

// Open WebSocket connection to Kubernetes API server
func openConnection(endpoint string) *websocket.Conn {	
	u, err := url.Parse(endpoint)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Connect to K8S master host: %v", u.Host)
	rawConn, err := net.Dial("tcp", u.Host)
	if err != nil {
		log.Fatal(err)
	}

	wsHeaders := http.Header{
		"Origin":                   {endpoint},
		"Sec-WebSocket-Extensions": {"permessage-deflate; client_max_window_bits, x-webkit-deflate-frame"},
	}

	wsConn, resp, err := websocket.NewClient(rawConn, u, wsHeaders, 1024, 1024)
	if err != nil {
		log.Fatalf("websocket.NewClient Error: %s\nResp:%+v", err, resp)

	}

	return wsConn
}

// Listen for Pods. We're only interested in MODIFIED and DELETED events.
func podsListener(wsConn *websocket.Conn, wsErrors chan string) {
	log.Println("Listening for pods")

	for {
		_, r, err := wsConn.NextReader()

		if err != nil {
			log.Printf("Error getting reader: %v",err)
			wsErrors <- "Error"
			return
		}

		dec := json.NewDecoder(r)
		
		var objmap map[string]*json.RawMessage
		dec.Decode(&objmap)
		
		// debug purpose
		//b,_ := json.Marshal(objmap)
		//log.Printf("%s", b)

		var actionType string
		json.Unmarshal(*objmap["type"], &actionType)

		var pod api.Pod
		err = json.Unmarshal(*objmap["object"], &pod)

		switch actionType {
			case "MODIFIED":
				registerPod(pod)
			case "DELETED":
				unregisterPod(pod)
		}
	}
}

// Listen for Services.
func svcListener(wsConn *websocket.Conn, wsErrors chan string) {
	log.Println("Listening for services")

	for {
		_, r, err := wsConn.NextReader()

		if err != nil {
			log.Printf("Error getting reader: %v",err)
			wsErrors <- "Error"
			return
		}

		dec := json.NewDecoder(r)
		
		var objmap map[string]*json.RawMessage
		dec.Decode(&objmap)
		
		// debug purpose
		// b,_ := json.Marshal(objmap)
		// log.Printf("%s", b)
				
		var actionType string
		json.Unmarshal(*objmap["type"], &actionType)

		var svc api.Service
		err = json.Unmarshal(*objmap["object"], &svc)

		switch actionType {
			case "ADDED":
				registerSvc(svc)
			case "DELETED":
				unregisterSvc(svc)
		}
	}
}

func registerPod(pod api.Pod) {
	if (pod.Status.Phase != "Running") {
		return
	}

	log.Printf("Inspect pod %v ...", pod.Name)

	for _, container := range pod.Spec.Containers {	
		for _, port := range container.Ports {
			if "TCP" != port.Protocol {
				continue // bypass non TCP port
			}
			
			for _, vport := range strings.Split(*argVulcandPorts, ",") {
				if vport == strconv.Itoa(port.ContainerPort) {
					hook( Endpoint{Name: pod.Name, Namespace: pod.Namespace, IP: pod.Status.PodIP, Port: port.ContainerPort} )
				}
			}
		}
	}
}

func unregisterPod(pod api.Pod) {
	unhook( Endpoint{Name: pod.Name, Namespace: pod.Namespace} )
}

func registerSvc(svc api.Service) {
	log.Printf("Inspect svc %v ...", svc.Name)

	for _, port := range svc.Spec.Ports {
		if "TCP" != port.Protocol {
			continue // bypass non TCP port
		}
		
		for _, vport := range strings.Split(*argVulcandPorts, ",") {
			if vport == strconv.Itoa(port.Port) {
				hook( Endpoint{Name: svc.Name, Namespace: svc.Namespace, IP: svc.Spec.ClusterIP, Port: port.Port} )
			}
		}
	}
}

func unregisterSvc(svc api.Service) {
	unhook( Endpoint{Name: svc.Name, Namespace: svc.Namespace} )
}

/*************
 *  Register a new backend server in Vulcan based on the Pod
 *
 *   Frontend: http://[pod].[namespace].k8s:[container-port]/ 
 *   Route to: http://[pod-ip]:[pod-container-port]
 *
 *  Valid container ports depends on '-port' parameter, default 8000
 *************/
func hook(e Endpoint) {
	log.Printf("Register %v listening on %v:%v", e.Name, e.IP, e.Port)

	// uuid name
	uuid := fmt.Sprintf("%v-%v-%v", e.Namespace, e.Name, e.Port)

	// set backend
	key := fmt.Sprintf("/vulcand/backends/%v/backend", uuid)
	value := fmt.Sprintf(`{"Type": "http"}`)

	_, err := kapi.Set(context.Background(), key, value, nil)
	if err != nil {
		log.Printf("Can't enable backend on key %v", key)
		if cerr, ok := err.(*client.ClusterError); ok {
			log.Print(cerr.Detail())
		} else {
			log.Print(err)
		}
		return
	}	

	// set backend server url
	key = fmt.Sprintf("/vulcand/backends/%v/servers/svc", uuid)
	value = fmt.Sprintf(`{"URL": "http://%v:%v"}`, e.IP, e.Port)

	_, err = kapi.Set(context.Background(), key, value, nil)
	if err != nil {
		log.Printf("Can't config backend on key %v", key)
		log.Print(err)
		return
	}

	// set frontend rule
	key = fmt.Sprintf("/vulcand/frontends/%v/frontend", uuid)
	rule := fmt.Sprintf("HostRegexp(`%v.%v.*`) && Port(`%v`)", e.Name, e.Namespace, e.Port)
	//rule := fmt.Sprintf("HostRegexp(`%v.%v.*`)", e.Name, e.Namespace)
	value = fmt.Sprintf(`{"Type": "http", "BackendId": "%v", "Route": "%v", "Settings": {"PassHostHeader": %v}}`, 
		uuid, rule, *argRetainHostHeader)

	_, err = kapi.Set(context.Background(), key, value, nil)
	if err != nil {
		log.Printf("Can't config frontend on key %v", key)
		log.Print(err)
		return
	}
}


// Remove a backend server from Vulcan when a Pod is deleted.
func unhook(e Endpoint) {
	log.Printf("Unregister %v", e.Name)

	// uuid name (prefix)
	uuid := fmt.Sprintf("%v-%v", e.Namespace, e.Name)

	// remove frontend
	r, err := kapi.Get(context.Background(), "/vulcand/frontends", nil)
	if err != nil {
		log.Println("Can't get frontend list")
        log.Print(err)
		return
    }
	
	for _, node := range r.Node.Nodes {
        if strings.Contains(node.Key, uuid) {
			// match key, remove it recursively
            _, err = kapi.Delete(context.Background(), node.Key, &client.DeleteOptions{Recursive: true})
            if err != nil {
				log.Print(err)
            }
		} 
    }

	// remove backend
	r, err = kapi.Get(context.Background(), "/vulcand/backends", nil)
	if err != nil {
		log.Println("Can't get backend list")
        log.Print(err)
		return
    }
	
	for _, node := range r.Node.Nodes {
        if strings.Contains(node.Key, uuid) {
			// match key, remove it recursively
            _, err = kapi.Delete(context.Background(), node.Key, &client.DeleteOptions{Recursive: true})
            if err != nil {
				log.Print(err)
            }
		}
    }
}

func addListenPorts() {
	log.Printf("Enable extra listen port of Vulcan, %v", *argVulcandPorts)

	// remove previous listen ports
	_, err := kapi.Delete(context.Background(), "/vulcand/listeners", &client.DeleteOptions{Recursive: true})
	if err != nil {
		log.Print(err)
	}
	
	// add new listen ports
	for i, vport := range strings.Split(*argVulcandPorts, ",") {
		if 0 == i {
			continue // we don't register default/first port to avoid vulcand conflict/crash
		}
		key := fmt.Sprintf("/vulcand/listeners/%v", vport)
		value := fmt.Sprintf(`{"Protocol":"http", "Address":{"Network":"tcp", "Address":"0.0.0.0:%v"}}`, vport)
	
		_, err := kapi.Set(context.Background(), key, value, nil)
		if err != nil {
			log.Printf("Can't create listen port on key %v", key)
			log.Print(err)
		}
    }
}