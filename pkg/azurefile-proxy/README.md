# Azurefile Proxy Development

> azurefile-proxy start unix socket under `/var/lib/kubelet/plugins/file.csi.azure.com/azurefile-proxy.sock` by default

 - make sure all required [Protocol Buffers](https://github.com/protocolbuffers/protobuf) binaries are installed
```console
./hack/install-protoc.sh
```
 - when any change is made to `proto/*.proto` file, run below command to generate
```console
rm pkg/azurefile-proxy/pb/*.go
protoc --proto_path=pkg/azurefile-proxy/proto --go-grpc_out=pkg/azurefile-proxy/pb --go_out=pkg/azurefile-proxy/pb pkg/azurefile-proxy/proto/*.proto
```
 - build new azurefile-proxy binary by running
```console
make azurefile-proxy
```

