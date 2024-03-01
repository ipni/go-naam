package naam

import (
	"bytes"
	"io"
	"net/http"
	"time"

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

const defaultIndexerURL = "https://cid.contact"

type (
	Option  func(*options)
	options struct {
		ds             datastore.Datastore
		host           host.Host
		httpClient     *http.Client
		httpFindURL    string
		httpIndexerURL string
		httpListenAddr string
		linkSys        *ipld.LinkSystem
		readPriv       bool
	}

	PublishOption  func(*publishOptions)
	publishOptions struct {
		embedPubKey bool
		eol         time.Time
		ttl         time.Duration
	}
)

func newOptions(o ...Option) (*options, error) {
	opts := options{
		httpClient:     http.DefaultClient,
		httpIndexerURL: defaultIndexerURL,
		httpListenAddr: "0.0.0.0:8080",
		readPriv:       true,
	}
	for _, apply := range o {
		apply(&opts)
	}

	var err error
	if opts.host == nil {
		opts.host, err = libp2p.New()
		if err != nil {
			return nil, err
		}
	}
	if opts.ds == nil {
		opts.ds = sync.MutexWrap(datastore.NewMapDatastore())
	}
	if opts.linkSys == nil {
		opts.linkSys = makeLinkSys(opts.ds)
	}

	if opts.httpFindURL == "" {
		opts.httpFindURL = opts.httpIndexerURL
	}

	return &opts, nil
}

func makeLinkSys(ds datastore.Datastore) *ipld.LinkSystem {
	linkSys := cidlink.DefaultLinkSystem()
	lsds := namespace.Wrap(ds, datastore.NewKey("ls"))
	linkSys.StorageReadOpener = func(ctx linking.LinkContext, l datamodel.Link) (io.Reader, error) {
		val, err := lsds.Get(ctx.Ctx, datastore.NewKey(l.Binary()))
		if err != nil {
			return nil, err
		}
		return bytes.NewBuffer(val), nil
	}
	linkSys.StorageWriteOpener = func(ctx ipld.LinkContext) (io.Writer, ipld.BlockWriteCommitter, error) {
		buf := bytes.NewBuffer(nil)
		return buf, func(l ipld.Link) error {
			return lsds.Put(ctx.Ctx, datastore.NewKey(l.Binary()), buf.Bytes())
		}, nil
	}
	return &linkSys
}

// WithDatastore supplies an existing datastore. An in-memory datastore is
// created if one is not supplied.
func WithDatastore(ds datastore.Datastore) Option {
	return func(o *options) {
		o.ds = ds
	}
}

// WithHost supplies a libp2p Host. If not specified a new Host is created.
func WithHost(h host.Host) Option {
	return func(o *options) {
		o.host = h
	}
}

// WithHttpClient supplies an optional HTTP client. If not specified, the
// default client is used.
func WithHttpClient(c *http.Client) Option {
	return func(o *options) {
		o.httpClient = c
	}
}

// WithHttpFindURL configures the base URL use for lookups. Use this when the
// lookup URL is different from the ingest URL configured by
// WithHttpIndexerURL. This may be needed when the lookup URL has a different
// port or is a dhstore URL.
func WithHttpFindURL(a string) Option {
	return func(o *options) {
		if a != "" {
			o.httpFindURL = a
		}
	}
}

// WithHttpIndexerURL configures the indexer base URL for sending announce
// messages to. This URL is also used for metadata lookup. If a different
// URL is needed for lookup, specify using WithHttpFindURL. If no values
// are specified, then defaultIndexerURL is used.
func WithHttpIndexerURL(a string) Option {
	return func(o *options) {
		if a != "" {
			o.httpIndexerURL = a
		}
	}
}

// WithHttpListenAddr specifies the listen address of the HTTP server that
// serves IPNI addresses. If not specified, the default "0.0.0.0:8080" is used.
func WithHttpListenAddr(a string) Option {
	return func(o *options) {
		if a != "" {
			o.httpListenAddr = a
		}
	}
}

func WithLinkSystem(ls *ipld.LinkSystem) Option {
	return func(o *options) {
		o.linkSys = ls
	}
}

// WithReaderPrivacy enables (true) or disables (false) reader-privacy. This is
// enabled by default.
func WithReaderPrivacy(enabled bool) Option {
	return func(o *options) {
		o.readPriv = enabled
	}
}

func newPublishOptions(o ...PublishOption) *publishOptions {
	opts := publishOptions{
		eol:         time.Now().Add(24 * time.Hour),
		embedPubKey: true,
	}
	for _, apply := range o {
		apply(&opts)
	}
	return &opts
}

func WithEmbedPublicKey(epk bool) PublishOption {
	return func(o *publishOptions) {
		o.embedPubKey = epk
	}
}

func WithEOL(eol time.Time) PublishOption {
	return func(o *publishOptions) {
		o.eol = eol
	}
}

func WithTTL(ttl time.Duration) PublishOption {
	return func(o *publishOptions) {
		o.ttl = ttl
	}
}
