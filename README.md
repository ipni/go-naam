# go-naam

:construction: Experimental.

The Golang implementation of [IPNI Naam protocol](https://github.com/ipni/specs/pull/4).

Publish and resolve [IPNS](https://specs.ipfs.tech/ipns/ipns-record/) records on top of IPNI advertisement change.

## Example

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ipfs/boxo/path"
	"github.com/ipfs/go-cid"
	"github.com/ipni/go-naam"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	announceURL = "http://localhost:3001"  // indexer ingest URL (e.g. "https//dev.cid.contact")
	findURL     = "http://localhost:40080" // dhstore service URL (e.g. "https://assigner.dev.cid.contact")
)

func main() {
	ctx := context.TODO()
	someCid, err := cid.Decode("bafzaajaiaejcbzdibmxyzdjbbehgvizh6g5tikvy47mshdy6gwbruvgwvd24seje")
	if err != nil {
		panic(err)
	}

	h, err := libp2p.New()
	if err != nil {
		panic(err)
	}
	n, err := naam.New(naam.WithHost(h),
		naam.WithHttpIndexerURL(announceURL),
		naam.WithHttpFindURL(findURL),
	)
	if err != nil {
		panic(err)
	}

	// Publish IPNS record to IPNI indexer.
	publishedPath := path.FromCid(someCid)
	err = n.Publish(ctx, publishedPath, naam.WithEOL(time.Now().Add(48*time.Hour)))
	if err != nil {
		panic(err)
	}
	ipnsName := n.Name()
	fmt.Println("IPNS record published with name", ipnsName)

	// Resolve locally - avoids indexer lookup if naam instance is the publisher.
	resolvedPath, err := n.Resolve(ctx, ipnsName)
	if err != nil {
		panic(err)
	}
	fmt.Println("Resolved IPNS record locally:", ipnsName, "==>", resolvedPath)

retry:
	time.Sleep(time.Second)

	// Resolve by looking up IPNS record using indexer with reader-privacy.
	resolvedPath, err = naam.Resolve(ctx, ipnsName, findURL)
	if err != nil {
		if errors.Is(err, naam.ErrNotFound) {
			fmt.Println("Name not found on indexer yet, retrying")
			goto retry
		}
		panic(err)
	}
	fmt.Println("🔒 Reader privacy enabled | Resolved IPNS record using indexer:", ipnsName, "==>", resolvedPath)

	// Resolve by looking up IPNS record using indexer without reader-privacy.
	resolvedPath, err = naam.ResolveNotPrivate(ctx, ipnsName, findURL)
	if err != nil {
		panic(err)
	}
	fmt.Println("⚠️  Reader privacy disabled | Resolved IPNS record using indexer:", ipnsName, "==>", resolvedPath)

	// Resolve a name that does not have an IPNS record.
	pid, err := peer.Decode("12D3KooWPbQ26UtFJ48ybpCyUoFYFBqH64DbHGMAKtXobKtRdzFF")
	if err != nil {
		panic(err)
	}
	anotherName := naam.Name(pid)
	resolvedPath, err = naam.Resolve(ctx, anotherName, findURL)
	if !errors.Is(err, naam.ErrNotFound) {
		panic(err)
	}
	fmt.Println("Record for unknown name", anotherName, "not found, as expected")
}
```

## License

[SPDX-License-Identifier: Apache-2.0 OR MIT](LICENSE.md)
