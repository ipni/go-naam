package naam

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/ipfs/boxo/ipns"
	"github.com/ipfs/boxo/path"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	"github.com/ipld/go-ipld-prime"
	_ "github.com/ipld/go-ipld-prime/codec/dagcbor"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/ipni/go-libipni/announce"
	"github.com/ipni/go-libipni/announce/httpsender"
	"github.com/ipni/go-libipni/dagsync/ipnisync"
	"github.com/ipni/go-libipni/find/client"
	"github.com/ipni/go-libipni/ingest/schema"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multicodec"
	"github.com/multiformats/go-multihash"
	"github.com/multiformats/go-varint"
)

// ErrNotFound is returned when naam fails to find the requested IPNS record.
var ErrNotFound = errors.New("naam: not found")

var (
	MetadataProtocolID = multicodec.IpnsRecord
	ContextID          = []byte("/ipni/naam")

	ls = cidlink.LinkPrototype{
		Prefix: cid.Prefix{
			Version: 1,
			//Codec:    uint64(multicodec.DagCbor),
			Codec:    uint64(multicodec.DagJson),
			MhType:   uint64(multicodec.Sha2_256),
			MhLength: -1,
		},
	}

	headAdCid = datastore.NewKey("headAdCid")
	height    = datastore.NewKey("height")
)

type Naam struct {
	*options
	httpAnnouncer *httpsender.Sender
}

// New creates a new Naam instance for publishing IPNS records in an indexer.
func New(o ...Option) (*Naam, error) {
	opts, err := newOptions(o...)
	if err != nil {
		return nil, err
	}

	// Create publisher that publishes over http and libptp.
	pk := opts.h.Peerstore().PrivKey(opts.h.ID())
	opts.pub, err = ipnisync.NewPublisher(*opts.ls, pk,
		ipnisync.WithHTTPListenAddrs(opts.httpListenAddr),
		ipnisync.WithStreamHost(opts.h),
	)
	if err != nil {
		return nil, err
	}

	indexerURL, err := url.Parse(opts.httpIndexerURL)
	if err != nil {
		return nil, err
	}

	// Create a direct http announcement sender.
	httpAnnouncer, err := httpsender.New([]*url.URL{indexerURL}, opts.h.ID(),
		httpsender.WithClient(opts.httpClient))
	if err != nil {
		return nil, err
	}

	return &Naam{
		options:       opts,
		httpAnnouncer: httpAnnouncer,
	}, nil
}

// Name returns the key to lookup the IPNS record for the given peer ID.
func Name(peerID peer.ID) string {
	return ipns.NamespacePrefix + peerID.String()
}

// Resolve performs a reader-privacy-enabled indexer lookup of the name and
// returns the IPFS path identified by the name. The multihash lookup request
// is sent to the host specified in findURL.
func Resolve(ctx context.Context, name string, findURL string) (path.Path, error) {
	finder, err := client.NewDHashClient(
		client.WithDHStoreURL(findURL),
		client.WithMetadataOnly(true))
	if err != nil {
		return nil, err
	}
	return resolve(ctx, name, finder)
}

// ResolveNotPrivate performs an indexer lookup of the name, without reader
// privacy, and returns the IPFS path identified by the name. The multihash
// lookup request is sent to the host specified by findURL.
func ResolveNotPrivate(ctx context.Context, name string, findURL string) (path.Path, error) {
	finder, err := client.New(findURL)
	if err != nil {
		return nil, err
	}
	return resolve(ctx, name, finder)
}

// Name returns the key to lookup the IPNS record published by this Naam
// instance.
func (n *Naam) Name() string {
	return Name(n.h.ID())
}

// Resolve returns the IPFS path identified by the specified name.
//
// If name matches that of this Naam instance, it is fetched from the local
// datastore. Otherwise, Resolve performs an indexer lookup of the name.
// Reader-privacy is enabled by default unless the option
// WithReaderPrivacy(false) was specified.
//
// If doing an indexer lookup, the multihash lookup request is sent to the host
// specified in the WithHttpFindURL option, or if not specified, then in
// WithHttpIndexerURL.
func (n *Naam) Resolve(ctx context.Context, name string) (path.Path, error) {
	p, err := path.NewPath(name)
	if err != nil {
		// Given name is not a valid IPFS path.
		return nil, err
	}
	if isJustAKey(p) {
		// IPFS path constructed from name is already resolved; nothing to do.
		return p, nil
	}

	pid, err := peerIDFromName(name)
	if err != nil {
		return nil, err
	}

	var metadata []byte
	if pid == n.h.ID() {
		// This is the ID of this host, so load advertisement from storage.
		head, err := n.getHeadAdCid(ctx)
		if err != nil {
			return nil, err
		}
		if head == cid.Undef {
			// TODO return a better error?
			return nil, ErrNotFound
		}

		n, err := n.ls.Load(ipld.LinkContext{Ctx: ctx}, cidlink.Link{Cid: head}, schema.AdvertisementPrototype)
		if err != nil {
			return nil, err
		}
		advertisement, err := schema.UnwrapAdvertisement(n)
		if err != nil {
			return nil, err
		}
		metadata = advertisement.Metadata
	} else {
		var finder client.Finder
		if n.readPriv {
			finder, err = client.NewDHashClient(
				client.WithClient(n.httpClient),
				client.WithDHStoreURL(n.httpFindURL),
				client.WithMetadataOnly(true))
		} else {
			finder, err = client.New(n.httpFindURL, client.WithClient(n.httpClient))
		}
		if err != nil {
			return nil, err
		}

		// Lookup metadata using indexer.
		metadata, err = lookupNaamMetadata(ctx, pid, finder)
		if err != nil {
			return nil, err
		}
	}

	return metadataToPath(metadata, name)
}

func resolve(ctx context.Context, name string, finder client.Finder) (path.Path, error) {
	p, err := path.NewPath(name)
	if err != nil {
		// Given name is not a valid IPFS path.
		return nil, err
	}
	if isJustAKey(p) {
		// IPFS path constructed from name is already resolved; nothing to do.
		return p, nil
	}

	pid, err := peerIDFromName(name)
	if err != nil {
		return nil, err
	}

	// Lookup metadata using indexer.
	metadata, err := lookupNaamMetadata(ctx, pid, finder)
	if err != nil {
		return nil, err
	}

	return metadataToPath(metadata, name)
}

// isJustAKey returns true if the path is of the form <key> or /ipfs/<key>, or
// /ipld/<key>
func isJustAKey(p path.Path) bool {
	parts := p.Segments()
	return len(parts) == 2 && (parts[0] == "ipfs" || parts[0] == "ipld")
}

func peerIDFromName(name string) (peer.ID, error) {
	spid := strings.TrimPrefix(name, ipns.NamespacePrefix)
	if spid == name {
		// Missing `/ipns/` prefix.
		return "", ipns.ErrInvalidName
	}
	return peer.Decode(spid)
}

// metadataToPath extracts the IPNS record from the metadata, validates it, and
// returns the path contained in the metadata.
//
// The record must be signed by the private key that matches the public key
// that the name was created with. Therefore, it is critical that the revord is
// validated with the public key from the IPNS name, not from the IPNS record.
func metadataToPath(metadata []byte, name string) (path.Path, error) {
	ipnsRec, err := ipnsFromMetadata(metadata)
	if err != nil {
		return nil, err
	}

	ipnsName, err := ipns.NameFromString(name)
	if err != nil {
		return nil, err
	}

	if err = ipns.ValidateWithName(ipnsRec, ipnsName); err != nil {
		return nil, err
	}

	return ipnsRec.Value()
}

// Publish creates a new IPNI advertisement containing an IPNS record for the
// given path value and a multihash key for the peer. The advertisement is
// published to the configured indexer.
//
// This allows the peer multihash to lookup an IPNS record using an IPNI
// indexer. The IPNS record is then resolved to the path that was published in
// the IPNI advertisement.
func (n *Naam) Publish(ctx context.Context, value path.Path, o ...PublishOption) error {
	opts, err := newPublishOptions(o...)
	if err != nil {
		return err
	}

	var prevLink ipld.Link
	head, err := n.getHeadAdCid(ctx)
	if err != nil {
		return err
	}
	if head != cid.Undef {
		prevLink = cidlink.Link{Cid: head}
	}

	prevHeight, err := n.previousHeight(ctx)
	if err != nil {
		return err
	}
	seq := prevHeight + 1

	pid := n.h.ID()
	pk := n.h.Peerstore().PrivKey(pid)
	ipnsRec, err := ipns.NewRecord(pk, value, seq, opts.eol, opts.ttl, ipns.WithPublicKey(opts.embedPubKey))
	if err != nil {
		return err
	}

	// Store entry block.
	mh, err := multihash.Sum([]byte(pid), multihash.SHA2_256, -1)
	if err != nil {
		return err
	}
	chunk := schema.EntryChunk{
		Entries: []multihash.Multihash{mh},
	}
	cn, err := chunk.ToNode()
	if err != nil {
		return err
	}
	entriesLink, err := n.ls.Store(ipld.LinkContext{Ctx: ctx}, ls, cn)
	if err != nil {
		return err
	}

	metadata, err := ipnsMetadata(ipnsRec)
	if err != nil {
		return err
	}
	ad := schema.Advertisement{
		PreviousID: prevLink,
		Provider:   pid.String(),
		Addresses:  n.adAddrs(),
		Entries:    entriesLink,
		ContextID:  ContextID,
		Metadata:   metadata,
	}
	if err := ad.Sign(pk); err != nil {
		return err
	}

	adn, err := ad.ToNode()
	if err != nil {
		return err
	}
	adLink, err := n.ls.Store(ipld.LinkContext{Ctx: ctx}, ls, adn)
	if err != nil {
		return err
	}

	newHead := adLink.(cidlink.Link).Cid
	n.pub.SetRoot(newHead)
	if err := n.setHeadAdCid(ctx, newHead); err != nil {
		return err
	}

	err = announce.Send(ctx, newHead, n.pub.Addrs(), n.httpAnnouncer)
	if err != nil {
		return fmt.Errorf("unsuccessful announce: %w", err)
	}
	return nil
}

func (n *Naam) adAddrs() []string {
	pa := n.pub.Addrs()
	adAddrs := make([]string, 0, len(pa))
	for _, a := range pa {
		adAddrs = append(adAddrs, a.String())
	}
	return adAddrs
}

func (n *Naam) setHeadAdCid(ctx context.Context, head cid.Cid) error {
	if err := n.ds.Put(ctx, headAdCid, head.Bytes()); err != nil {
		return err
	}
	h, err := n.previousHeight(ctx)
	if err != nil {
		return err
	}
	return n.ds.Put(ctx, height, varint.ToUvarint(h+1))
}

func (n *Naam) previousHeight(ctx context.Context) (uint64, error) {
	v, err := n.ds.Get(ctx, height)
	if err != nil {
		if err == datastore.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}
	buf := bytes.NewBuffer(v)
	return varint.ReadUvarint(buf)
}

func (n *Naam) getHeadAdCid(ctx context.Context) (cid.Cid, error) {
	c, err := n.ds.Get(ctx, headAdCid)
	if err != nil {
		if err == datastore.ErrNotFound {
			return cid.Undef, nil
		}
		return cid.Undef, err
	}
	_, head, err := cid.CidFromBytes(c)
	if err != nil {
		return cid.Undef, nil
	}
	return head, nil
}

func ipnsMetadata(rec *ipns.Record) ([]byte, error) {
	var metadata bytes.Buffer
	metadata.Write(varint.ToUvarint(uint64(MetadataProtocolID)))
	marshal, err := ipns.MarshalRecord(rec)
	if err != nil {
		return nil, err
	}
	metadata.Write(marshal)
	return metadata.Bytes(), nil
}

func ipnsFromMetadata(md []byte) (*ipns.Record, error) {
	buf := bytes.NewBuffer(md)
	protoID, err := varint.ReadUvarint(buf)
	if err != nil {
		return nil, err
	}
	if protoID != uint64(MetadataProtocolID) {
		return nil, fmt.Errorf("expected protocol ID %d but got %d", MetadataProtocolID, protoID)
	}

	return ipns.UnmarshalRecord(buf.Bytes())
}

func lookupNaamMetadata(ctx context.Context, pid peer.ID, finder client.Finder) ([]byte, error) {
	mh, err := multihash.Sum([]byte(pid), multihash.SHA2_256, -1)
	if err != nil {
		return nil, err
	}

	resp, err := finder.Find(ctx, mh)
	if err != nil {
		return nil, err
	}

	if resp != nil {
		for _, mhr := range resp.MultihashResults {
			if bytes.Equal(mhr.Multihash, mh) {
				for _, pr := range mhr.ProviderResults {
					if pr.Provider.ID == pid && bytes.Equal(pr.ContextID, ContextID) {
						return pr.Metadata, nil
					}
				}
			}
		}
	}
	return nil, ErrNotFound
}
