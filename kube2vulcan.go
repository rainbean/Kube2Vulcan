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
	"github.com/coreos/go-etcd/etcd"
	"fmt"
	"strings"
	"time"
)

var k8sAddress string
var etcdAddress string

// Always called before main(), per package 
func init() {
	flag.StringVar(&k8sAddress, "master", "", "Address of Kubernetes API server")
	flag.StringVar(&etcdAddress, "etcd", "", "Address of etcd of Vulcan daemon")

	flag.Parse()
	
	if k8sAddress == "" || etcdAddress == "" {
		log.Fatal(`Missing required properties. Usage: -master "[k8s-master-ip]:[port]" -etcd "http://[etcd-ip]:[port],http://[2nd-etcd-ip]:[port],..."`)
	}
}

// Main function
// Connect to Kubernetes API server and monitor for PODS/SVC events, 
// then sync to etcd in syntax of vulcand
func main() {
	var wsPodsErrors chan string = make(chan string)
	var wsSvcErrors chan string = make(chan string)

	podsEndpoint := fmt.Sprintf("ws://%v/api/v1/pods?watch=true", k8sAddress)
	svcEndpoint := fmt.Sprintf("ws://%v/api/v1/services?watch=true", k8sAddress)
	
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

/*************
 *  Register a new backend server in Vulcan based on the new Pod
 *  // *** Pods as a App ***
 *   Frontend: http://[pods].[namespace].k8s:8000/ 
 *   Route to: http://[pods-ip]:[pods-first-tcp-container-port]
 *
 *   Backend : /vulcand/backends/[namespace]-[pods]/servers/srv '{"URL": "http://[pods-ip]:[pods-first-tcp-container-port]"}'
 *
 *  ToDo: current configuration only listen at 8000 port
 *************/
func registerPod(pod api.Pod) {
	if (pod.Status.Phase != "Running") {
		return
	}

	idx := -1
	
	for i,port := range pod.Spec.Containers[0].Ports {
		if "TCP" != port.Protocol {
			continue // bypass non TCP port
		} else if 443 == port.ContainerPort || 8443 == port.ContainerPort {
			continue // bypass HTTPS ports
		} else {
			idx = i
			break // match first proper port
		}
    }

	if -1 == idx {
		log.Printf("No proper ports exposed by container %v, skipping registration", pod.Name)
		return
	}
	
	if "TCP" != pod.Spec.Containers[0].Ports[idx].Protocol {
		log.Printf("First exposed port is not TCP by container %v, skipping registration", pod.Name)
		return
	}

	log.Printf("Register pod %v listening on %v:%v", 
		pod.Name, pod.Status.PodIP, pod.Spec.Containers[0].Ports[idx].ContainerPort)

	// Get Etcd client
	client := etcd.NewClient(strings.Split(etcdAddress, ","))
	uuid := fmt.Sprintf("%v-%v", pod.Namespace, pod.Name)

	key := fmt.Sprintf("/vulcand/backends/%v/backend", uuid)
	value := fmt.Sprintf(`{"Type": "http"}`)
	_, err := client.Set(key, value, 0);
	if err != nil {
		log.Printf("Can't enable backend on key %v", key)
		log.Print(err)
		return
	}

	key = fmt.Sprintf("/vulcand/backends/%v/servers/svc", uuid)
	value = fmt.Sprintf(`{"URL": "http://%v:%v"}`, pod.Status.PodIP, pod.Spec.Containers[0].Ports[idx].ContainerPort)

	_, err = client.Set(key, value, 0);
	if err != nil {
		log.Printf("Can't config backend on key %v", key)
		log.Fatal(err)
		return
	}

	key = fmt.Sprintf("/vulcand/frontends/%v/frontend", uuid)
	value = fmt.Sprintf(`{"Type": "http", "BackendId": "%v", "Route": "HostRegexp(` + "`%v.%v.*`" + `)"}`, uuid, pod.Name, pod.Namespace)

	_, err = client.Set(key, value, 0);
	if err != nil {
		log.Printf("Can't config backend on key %v", key)
		log.Fatal(err)
		return
	}
}

// Remove a backend server from Vulcan when a Pod is deleted.
func unregisterPod(pod api.Pod) {
	log.Printf("Unregister pod %v listening on %v", 
		pod.Name, pod.Status.PodIP)

	// Get Etcd client
	client := etcd.NewClient(strings.Split(etcdAddress, ","))

	uuid := fmt.Sprintf("%v-%v", pod.Namespace, pod.Name)

	key := fmt.Sprintf("/vulcand/backends/%v/backend", uuid)
	_, err := client.Delete(key, false)
	if err != nil {
		log.Printf("Failed to delete entry '%v'. It might already be removed", key);
		log.Println(err)
	}

	key = fmt.Sprintf("/vulcand/backends/%v/servers/svc", uuid)
	_, err = client.Delete(key, false)
	if err != nil {
		log.Printf("Failed to delete entry '%v'. It might already be removed", key);
		log.Println(err)
	}

	key = fmt.Sprintf("/vulcand/frontends/%v/frontend", uuid)
	_, err = client.Delete(key, false)
	if err != nil {
		log.Printf("Failed to delete entry '%v'. It might already be removed", key);
		log.Println(err)
	}
}



/*************
 *  Register a new backend server in Vulcan based on the new Service
 *  // *** Service as a App ***
 *   Frontend: http://[service].[namespace].k8s:8000/ 
 *   Route to: http://[service-ip]:[service-first-tcp-port]
 *
 *   Backend : /vulcand/backends/[namespace]-[service]/servers/srv '{"URL": "http://[service-ip]:[service-first-tcp-port]"}'
 *
 *  ToDo: current configuration only listen at 8000 port
 *************/
func registerSvc(svc api.Service) {
	idx := -1
	
	for i,port := range svc.Spec.Ports {
		if "TCP" != port.Protocol {
			continue // bypass non TCP port
		} else if 443 == port.Port || 8443 == port.Port {
			continue // bypass HTTPS ports
		} else {
			idx = i
			break // match first proper port
		}
    }

	if -1 == idx {
		log.Printf("No proper ports exposed by service %v, skipping registration", svc.Name)
		return
	}

	log.Printf("Register service %v listening on %v:%v", 
		svc.Name, svc.Spec.ClusterIP, svc.Spec.Ports[idx].Port)

	// Get Etcd client
	client := etcd.NewClient(strings.Split(etcdAddress, ","))
	uuid := fmt.Sprintf("%v-%v", svc.Namespace, svc.Name)

	key := fmt.Sprintf("/vulcand/backends/%v/backend", uuid)
	value := fmt.Sprintf(`{"Type": "http"}`)
	_, err := client.Set(key, value, 0);
	if err != nil {
		log.Printf("Can't enable backend on key %v", key)
		log.Print(err)
		return
	}

	key = fmt.Sprintf("/vulcand/backends/%v/servers/svc", uuid)
	value = fmt.Sprintf(`{"URL": "http://%v:%v"}`, svc.Spec.ClusterIP, svc.Spec.Ports[idx].Port)

	_, err = client.Set(key, value, 0);
	if err != nil {
		log.Printf("Can't config backend on key %v", key)
		log.Fatal(err)
		return
	}

	key = fmt.Sprintf("/vulcand/frontends/%v/frontend", uuid)
	value = fmt.Sprintf(`{"Type": "http", "BackendId": "%v", "Route": "HostRegexp(` + "`%v.%v.*`" + `)"}`, uuid, svc.Name, svc.Namespace)

	_, err = client.Set(key, value, 0);
	if err != nil {
		log.Printf("Can't config backend on key %v", key)
		log.Fatal(err)
		return
	}
}

// Remove a backend server from Vulcan when a Service is deleted
func unregisterSvc(svc api.Service) {
	log.Printf("Unregister svc %v listening on %v", 
		svc.Name, svc.Spec.ClusterIP)

	// Get Etcd client
	client := etcd.NewClient(strings.Split(etcdAddress, ","))

	uuid := fmt.Sprintf("%v-%v", svc.Namespace, svc.Name)

	key := fmt.Sprintf("/vulcand/backends/%v/backend", uuid)
	_, err := client.Delete(key, false)
	if err != nil {
		log.Printf("Failed to delete entry '%v'. It might already be removed", key);
		log.Println(err)
	}

	key = fmt.Sprintf("/vulcand/backends/%v/servers/svc", uuid)
	_, err = client.Delete(key, false)
	if err != nil {
		log.Printf("Failed to delete entry '%v'. It might already be removed", key);
		log.Println(err)
	}

	key = fmt.Sprintf("/vulcand/frontends/%v/frontend", uuid)
	_, err = client.Delete(key, false)
	if err != nil {
		log.Printf("Failed to delete entry '%v'. It might already be removed", key);
		log.Println(err)
	}
}