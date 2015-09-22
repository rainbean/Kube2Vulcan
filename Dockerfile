FROM busybox
ADD Kube2Vulcan /Kube2Vulcan
ENTRYPOINT ["/Kube2Vulcan"]
