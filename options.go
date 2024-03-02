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

const defaultAnnounceURL = "https://cid.contact"

type (
	Option  func(*options)
	options struct {
		ds             datastore.Datastore
		host           host.Host
		httpClient     *http.Client
		findURL        string
		announceURL    string
		listenAddr     string
		linkSys        *ipld.LinkSystem
		providerAddrs  []string
		publisherAddrs []string
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
		httpClient:  http.DefaultClient,
		announceURL: defaultAnnounceURL,
		listenAddr:  "0.0.0.0:8080",
		readPriv:    true,
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

	if opts.findURL == "" {
		opts.findURL = opts.announceURL
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

// WithFindURL configures the URL use for lookups. This is the URL of the
// dhstore that is used for multihash lookup. If not specified the URL
// configured by WithAnnounceURL is used.
func WithFindURL(a string) Option {
	return func(o *options) {
		if a != "" {
			o.findURL = a
		}
	}
}

// WithAnnounceURL is the IPNI endpoint to send direct HTTP advertisement
// announcement messages to. If no values are specified, then the value of
// defaultAnnounceURL is used.
func WithAnnounceURL(a string) Option {
	return func(o *options) {
		if a != "" {
			o.announceURL = a
		}
	}
}

// WithListenAddr specifies the address:port that the publisher listens on when
// serving advertisements over HTTP. If not specified, the default
// "0.0.0.0:8080" is used.
func WithListenAddr(a string) Option {
	return func(o *options) {
		if a != "" {
			o.listenAddr = a
		}
	}
}

func WithLinkSystem(ls *ipld.LinkSystem) Option {
	return func(o *options) {
		o.linkSys = ls
	}
}

// WithPublisherAddrs is the multiaddr that tells IPNI where to fetch
// advertisements from.
//
// This address must be routable from the indexer. If not specified then the
// publisher listen addresses are used.
func WithPublisherAddrs(addrs ...string) Option {
	return func(o *options) {
		if len(addrs) != 0 {
			o.publisherAddrs = append(o.publisherAddrs, addrs...)
		}
	}
}

// WithReaderPrivacy enables (true) or disables (false) reader-privacy. This is
// enabled by default.
func WithReaderPrivacy(enabled bool) Option {
	return func(o *options) {
		o.readPriv = enabled
	}
}

// WithProviderAddrs are multiaddrs that get put into advertisements as the
// provider addresses. If not specified then the publisher addresses are used.
//
// When publishing to a public indexer, this should always be set to a public
// address so that the advertisement is not rejected.
//
// These addresses to not have a role in resolving IPNS, but may be used to
// tell an IPNS clients where to get the content corresponding to the CIR in
// the IPNS record.
func WithProviderAddrs(addrs ...string) Option {
	return func(o *options) {
		if len(addrs) != 0 {
			o.providerAddrs = append(o.providerAddrs, addrs...)
		}
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
