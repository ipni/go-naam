package naam

import (
	"bytes"
	"io"
	"net/http"
	"time"

	"github.com/filecoin-project/storetheindex/dagsync"
	"github.com/filecoin-project/storetheindex/dagsync/httpsync"
	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/namespace"
	"github.com/ipfs/go-datastore/sync"
	"github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/datamodel"
	"github.com/ipld/go-ipld-prime/linking"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
)

type (
	Option  func(*options) error
	options struct {
		h                   host.Host
		httpListenAddr      string
		httpIndexerEndpoint string
		httpClient          *http.Client
		ls                  *ipld.LinkSystem
		ds                  datastore.Datastore
		pub                 dagsync.Publisher
	}

	PublishOption  func(*publishOptions) error
	publishOptions struct {
		eol         time.Time
		ttl         time.Duration
		embedPubKey bool
	}
)

func newOptions(o ...Option) (*options, error) {
	opts := options{
		h:                   nil,
		httpListenAddr:      "0.0.0.0:8080",
		httpIndexerEndpoint: "https://cid.contact",
		httpClient:          http.DefaultClient,
	}
	for _, apply := range o {
		if err := apply(&opts); err != nil {
			return nil, err
		}
	}

	var err error
	if opts.h == nil {
		opts.h, err = libp2p.New()
		if err != nil {
			return nil, err
		}
	}
	if opts.ds == nil {
		opts.ds = sync.MutexWrap(datastore.NewMapDatastore())
	}
	if opts.ls == nil {
		ls := cidlink.DefaultLinkSystem()
		ds := namespace.Wrap(opts.ds, datastore.NewKey("ls"))
		ls.StorageReadOpener = func(ctx linking.LinkContext, l datamodel.Link) (io.Reader, error) {
			val, err := ds.Get(ctx.Ctx, datastore.NewKey(l.Binary()))
			if err != nil {
				return nil, err
			}
			return bytes.NewBuffer(val), nil
		}
		ls.StorageWriteOpener = func(ctx ipld.LinkContext) (io.Writer, ipld.BlockWriteCommitter, error) {
			buf := bytes.NewBuffer(nil)
			return buf, func(l ipld.Link) error {
				return ds.Put(ctx.Ctx, datastore.NewKey(l.Binary()), buf.Bytes())
			}, nil
		}
		opts.ls = &ls
	}
	pk := opts.h.Peerstore().PrivKey(opts.h.ID())
	opts.pub, err = httpsync.NewPublisher(opts.httpListenAddr, *opts.ls, opts.h.ID(), pk)
	if err != nil {
		return nil, err
	}
	return &opts, nil
}

func WithHost(h host.Host) Option {
	return func(o *options) error {
		o.h = h
		return nil
	}
}

func WithHttpListenAddr(a string) Option {
	return func(o *options) error {
		o.httpListenAddr = a
		return nil
	}
}

func WithHttpIndexerEndpoint(a string) Option {
	return func(o *options) error {
		o.httpIndexerEndpoint = a
		return nil
	}
}

func WithHttpClient(c *http.Client) Option {
	return func(o *options) error {
		o.httpClient = c
		return nil
	}
}

func WithLinkSystem(ls *ipld.LinkSystem) Option {
	return func(o *options) error {
		o.ls = ls
		return nil
	}
}

func WithDatastore(ds datastore.Datastore) Option {
	return func(o *options) error {
		o.ds = ds
		return nil
	}
}

func newPublishOptions(o ...PublishOption) (*publishOptions, error) {
	opts := publishOptions{
		eol:         time.Now().Add(24 * time.Hour),
		embedPubKey: true,
	}
	for _, apply := range o {
		if err := apply(&opts); err != nil {
			return nil, err
		}
	}
	return &opts, nil
}

func WithEOL(eol time.Time) PublishOption {
	return func(o *publishOptions) error {
		o.eol = eol
		return nil
	}
}

func WithTTL(ttl time.Duration) PublishOption {
	return func(o *publishOptions) error {
		o.ttl = ttl
		return nil
	}
}

func WithEmbedPublicKey(epk bool) PublishOption {
	return func(o *publishOptions) error {
		o.embedPubKey = epk
		return nil
	}
}
