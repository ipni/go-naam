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
	"github.com/ipni/go-libipni/dagsync"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
)

const defaultIndexerURL = "https://cid.contact"

type (
	Option  func(*options) error
	options struct {
		h              host.Host
		httpListenAddr string
		httpIndexerURL string
		httpFindURL    string
		httpClient     *http.Client
		ls             *ipld.LinkSystem
		ds             datastore.Datastore
		pub            dagsync.Publisher
		readPriv       bool
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
		h:              nil,
		httpIndexerURL: defaultIndexerURL,
		httpListenAddr: "0.0.0.0:8080",
		httpClient:     http.DefaultClient,
		readPriv:       true,
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

	if opts.httpFindURL == "" {
		opts.httpFindURL = opts.httpIndexerURL
	}

	return &opts, nil
}

// WithHost supplies a libp2p Host. If not specified a new Host is created.
func WithHost(h host.Host) Option {
	return func(o *options) error {
		o.h = h
		return nil
	}
}

// WithHttpListenAddr specifies the listen address of the HTTP server that
// serves IPNI addresses. If not specified, the default "0.0.0.0:8080" is used.
func WithHttpListenAddr(a string) Option {
	return func(o *options) error {
		if a != "" {
			o.httpListenAddr = a
		}
		return nil
	}
}

// WithHttpIndexerURL configures the indexer base URL for sending announce
// messages to. This URL is also used for metadata lookup. If a different
// URL is needed for lookup, specify using WithHttpFindURL. If no values
// are specified, then defaultIndexerURL is used.
func WithHttpIndexerURL(a string) Option {
	return func(o *options) error {
		if a != "" {
			o.httpIndexerURL = a
		}
		return nil
	}
}

// WithHttpFindURL configures the base URL use for lookups. Use this when the
// lookup URL is different from the ingest URL configured by
// WithHttpIndexerURL. This may be needed when the lookup URL has a different
// port or is a dhstore URL.
func WithHttpFindURL(a string) Option {
	return func(o *options) error {
		if a != "" {
			o.httpFindURL = a
		}
		return nil
	}
}

// WithHttpClient supplies an optional HTTP client. If not specified, the
// default client is used.
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

// WithReaderPrivacy enables (true) or disables (false) reader-privacy. This is
// enabled by default.
func WithReaderPrivacy(enabled bool) Option {
	return func(o *options) error {
		o.readPriv = enabled
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
