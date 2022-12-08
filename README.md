# go-naam

:construction: Experimental.

The Golang implementation of [IPNI NAAM protocol](https://github.com/ipni/specs/pull/4).

Publish and resolve IPNS records on top of IPNI advertisement change.

## Example

```go

ctx := context.TODO()
someCid, err := cid.Decode("bafzaajaiaejcbzdibmxyzdjbbehgvizh6g5tikvy47mshdy6gwbruvgwvd24seje")
if err != nil {
panic(err)
}

h, err := libp2p.New()
if err != nil {
panic(err)
}
n, err := naam.New(naam.WithHost(h))
if err != nil {
panic(err)
}
publishedPath := path.FromCid(someCid)
if err := n.Publish(ctx, publishedPath, naam.WithEOL(time.Now().Add(48*time.Hour))); err != nil {
panic(err)
}
ipnsKey := ipns.RecordKey(h.ID())
fmt.Println("IPNS record published with key", ipnsKey)

pid, err := peer.Decode("12D3KooWPbQ26UtFJ48ybpCyUoFYFBqH64DbHGMAKtXobKtRdzFF")
if err != nil {
panic(err)
}
anotherIpnsKey := ipns.RecordKey(pid)

resolvedPath, err := n.Resolve(ctx, anotherIpnsKey)
if err != nil {
panic(err)
}

fmt.Printf("Resolved IPNS record from %s to path %s\n", anotherIpnsKey, resolvedPath)

```

## License

[SPDX-License-Identifier: Apache-2.0 OR MIT](LICENSE.md)
