FROM scratch

ADD Kube2Vulcan /

ENTRYPOINT ["/Kube2Vulcan"]
