// Command releaseserve serves a directory of build artifacts the way GitHub
// Releases does, so the installers can be verified against the exact files a
// release is about to publish.
//
// Usage:
//
//	go run ./tools/releaseserve dist/ --tag v0.1.0 [--addr 127.0.0.1:0]
//
// It prints the base URL on the first line of stdout, so a caller that asked
// for port 0 can read the address it was given:
//
//	INTENTER_LATEST_URL=<base>/releases/latest
//	INTENTER_DOWNLOAD_BASE=<base>/releases/download
package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/Vadym903/Intenter/internal/releaseserve"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "address to listen on")
	tag := flag.String("tag", "", "release tag the directory contains, e.g. v0.1.0")
	flag.Usage = usage
	flag.Parse()

	if flag.NArg() != 1 {
		usage()
		os.Exit(2)
	}
	if *tag == "" {
		fmt.Fprintln(os.Stderr, "releaseserve: --tag is required")
		os.Exit(2)
	}

	handler, err := releaseserve.Handler(flag.Arg(0), *tag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "releaseserve: listen on %s: %v\n", *addr, err)
		os.Exit(1)
	}

	base := "http://" + listener.Addr().String()
	fmt.Println(base)
	fmt.Fprintf(os.Stderr, "releaseserve: serving %s as %s at %s\n", flag.Arg(0), *tag, base)

	if err := http.Serve(listener, handler); err != nil {
		fmt.Fprintln(os.Stderr, "releaseserve:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: releaseserve [--addr host:port] --tag vX.Y.Z <dir>")
	flag.PrintDefaults()
}
