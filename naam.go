package naam

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/filecoin-project/storetheindex/api/v0/ingest/schema"
	"github.com/gogo/protobuf/proto"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-ipns"
	ipns_pb "github.com/ipfs/go-ipns/pb"
	"github.com/ipfs/go-path"
	"github.com/ipld/go-ipld-prime"
	_ "github.com/ipld/go-ipld-prime/codec/dagcbor"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
	"github.com/multiformats/go-multicodec"
	"github.com/multiformats/go-multihash"
	"github.com/multiformats/go-varint"
)

var (
	MetadataProtocolID = multicodec.IpnsRecord
	ContextID          = []byte("/ipni/naam")

	ls = cidlink.LinkPrototype{
		Prefix: cid.Prefix{
			Version:  1,
			Codec:    uint64(multicodec.DagCbor),
			MhType:   uint64(multicodec.Sha2_256),
			MhLength: -1,
		},
	}

	headAdCid = datastore.NewKey("headAdCid")
	height    = datastore.NewKey("height")
)

type Naam struct {
	*options
}

func New(o ...Option) (*Naam, error) {
	opts, err := newOptions(o...)
	if err != nil {
		return nil, err
	}
	return &Naam{
		options: opts,
	}, nil
}

func (n *Naam) Resolve(ctx context.Context, name string) (value path.Path, err error) {
	p, err := path.ParsePath(name)
	switch {
	case err != nil:
		// Given name is not a valid IPFS path.
		return "", err
	case p.IsJustAKey():
		// IPFS path constructed from name is already resolved; nothing to do.
		return p, nil
	}

	spid := strings.TrimPrefix("/ipns/", p.String())
	if spid == p.String() {
		// Missing `/ipns/` prefix.
		return "", ipns.ErrInvalidPath
	}
	pid, err := peer.Decode(spid)
	if err != nil {
		return "", err
	}

	var metadata []byte
	if pid != n.h.ID() {
		mh, err := multihash.Sum([]byte(pid), multihash.SHA2_256, -1)
		if err != nil {
			return "", err
		}
		metadata, err = n.getNaamMetadata(ctx, pid, mh)
		if err != nil {
			return "", err
		}
	} else {
		head, err := n.getHeadAdCid(ctx)
		if err != nil {
			return "", err
		}
		if head == cid.Undef {
			// TODO return a better error?
			return "", routing.ErrNotFound
		}

		n, err := n.ls.Load(ipld.LinkContext{Ctx: ctx}, cidlink.Link{Cid: head}, schema.AdvertisementPrototype)
		if err != nil {
			return "", err
		}
		advertisement, err := schema.UnwrapAdvertisement(n)
		if err != nil {
			return "", err
		}
		metadata = advertisement.Metadata
	}
	entry, err := ipnsFromMetadata(metadata)
	if err != nil {
		return "", err
	}
	pubk, err := ipns.ExtractPublicKey(pid, entry)
	if err != nil {
		return "", err
	}
	if err := ipns.Validate(pubk, entry); err != nil {
		return "", err
	}
	return path.ParsePath(string(entry.Value))
}

func (n *Naam) Publish(ctx context.Context, value path.Path, o ...PublishOption) error {
	opts, err := newPublishOptions(o...)
	if err != nil {
		return err
	}

	var prevLink ipld.Link
	head, err := n.getHeadAdCid(ctx)
	if err != nil {
		return err
	} else if head != cid.Undef {
		prevLink = cidlink.Link{Cid: head}
	}

	prevHeight, err := n.previousHeight(ctx)
	if err != nil {
		return err
	}
	seq := prevHeight + 1

	pk := n.h.Peerstore().PrivKey(n.h.ID())
	entry, err := ipns.Create(pk, []byte(value), seq, opts.eol, opts.ttl)
	if err != nil {
		return err
	}
	if opts.embedPubKey {
		if err := ipns.EmbedPublicKey(pk.GetPublic(), entry); err != nil {
			return err
		}
	}

	pid, err := peer.IDFromPrivateKey(pk)
	if err != nil {
		return err
	}

	key := ipns.RecordKey(pid)
	mh, err := multihash.Sum([]byte(key), multihash.SHA2_256, -1)
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

	metadata, err := ipnsMetadata(entry)
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
	if err := n.pub.UpdateRoot(ctx, newHead); err != nil {
		return err
	}

	if err := n.setHeadAdCid(ctx, newHead); err != nil {
		return err
	}
	ai := &peer.AddrInfo{
		ID:    n.h.ID(),
		Addrs: n.pub.Addrs(),
	}
	return n.announce(ctx, ai, newHead)
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
		return 0, err
	}
	buf := bytes.NewBuffer(v)
	return varint.ReadUvarint(buf)
}

func (n *Naam) getHeadAdCid(ctx context.Context) (cid.Cid, error) {
	c, err := n.ds.Get(ctx, headAdCid)
	if err == datastore.ErrNotFound {
		return cid.Undef, nil
	}
	_, head, err := cid.CidFromBytes(c)
	if err != nil {
		return cid.Undef, nil
	}
	return head, nil
}

func ipnsMetadata(entry *ipns_pb.IpnsEntry) ([]byte, error) {
	var metadata bytes.Buffer
	metadata.Write(varint.ToUvarint(uint64(MetadataProtocolID)))
	marshal, err := proto.Marshal(entry)
	if err != nil {
		return nil, err
	}
	metadata.Write(marshal)
	return metadata.Bytes(), nil
}

func ipnsFromMetadata(md []byte) (*ipns_pb.IpnsEntry, error) {
	buf := bytes.NewBuffer(md)
	protoID, err := varint.ReadUvarint(buf)
	if err != nil {
		return nil, err
	}
	if protoID != uint64(MetadataProtocolID) {
		return nil, fmt.Errorf("expected protocol ID %d but got %d", MetadataProtocolID, protoID)
	}
	var entry ipns_pb.IpnsEntry
	if err := proto.Unmarshal(buf.Bytes(), &entry); err != nil {
		return nil, err
	}
	return &entry, nil
}
