# go-naam

:construction: Experimental.

The Golang implementation of [IPNI Naam protocol](https://github.com/ipni/specs/pull/4).

Publish and resolve [IPNS](https://specs.ipfs.tech/ipns/ipns-record/) records using IPNI advertisements and lookup.

## Overview

- [IPNS](https://specs.ipfs.tech/ipns/ipns-record/) specifies how a fixed key maps to an IPNS record (mutable pointer to object)
  - IPNS Name is a fixed key (hash of public key) that maps to an IPNS record
  - IPNS Record is mutable signed info about some object, such as an IPFS path.
  - Allows changing the object that an IPNS name refers to.
- [Naam](https://github.com/ipni/specs/pull/4/files) is a specification for how to use IPNI to resolve IPNS names to an IPNS records.
  - Specifies how to publish IPNS records to IPNI
  - Specifies how a client queries IPNI, using an IPNS name to lookup an IPNS record.

### Publishing IPNS

- The publisher creates a unique IPNS name to use. This is created from a public key, and the public key can be extracted from the IPNS name.
- The publisher creates an IPNS record that contains the CID of the object that the IPNS name resolves to. The record is signed using the private key that is associated with the public key used to create the name.
- The publisher creates an IPNI advertisement that contains the IPNS record data and the IONS name as a multihash lookup key for that record.
- The publisher announces the new advertisement to IPNI, and IPNI ingests the advertisement.
- Each unique IPNS name has its own chain of advertisements. The chain can be optionally kept as a historical record of the IPNS record updates.

### Resolving IPNS

- A client queries IPNI using the IPNS name in multihash form and receives an IPNS record in response.
- The client validates the signature of the IPNS record using the public key from the IPNS name. This prevents anyone, except the creator of the IPNS name, from being to publish IPNS records for that name. The resolved CID is extracted from the IPNS record and returned to the client.
- The client client then retrieves the data associated with that CID, possible from IPFS or other storage.

### Updating IPNS

- The publisher creates a new IPNS record with different data for the IPNS name to resolve to.
- The new IPNS record is published the same as before and announced to IPNS.
- Since the context ID and multhash key (IPNS name) are the same as previously, this results in only an update of the IPNS record, so there is no need to remove any previously published records.

### Removing IPNS

An IPNS record can be removed from IPNI by publishing a removal advertisement when there is no further use for the IPNS name. This is not strictly necessary since IPNS records support EOL (end-of-life) and TTL (time-to-live).

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
