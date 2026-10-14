// Command fakeregistry serves an in-memory Registry v2 endpoint with one sample
// image, so the proxy can be exercised manually against a real process. It is a
// development aid and is not part of the shipped product.
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/runui/oci-mirror-proxy/internal/testutil/fakeregistry"
)

func main() {
	s := fakeregistry.Start()
	defer s.Close()

	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{},"layers":[]}`)
	s.PutManifest("library/hello", "latest", manifest)
	layer := bytes.Repeat([]byte("hello-layer-"), 1024)
	digest := s.PutBlob(layer)

	fmt.Printf("url=%s\nmanifest=library/hello:latest\nblob=%s\n", s.URL, digest)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
}
