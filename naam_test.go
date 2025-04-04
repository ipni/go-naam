package naam_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ipfs/boxo/ipns"
	"github.com/ipfs/boxo/path"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/namespace"
	"github.com/ipfs/go-datastore/sync"
	testcmd "github.com/ipfs/go-test/cmd"
	"github.com/ipld/go-ipld-prime"
	_ "github.com/ipld/go-ipld-prime/codec/dagcbor"
	"github.com/ipld/go-ipld-prime/datamodel"
	"github.com/ipld/go-ipld-prime/linking"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/ipni/go-libipni/announce"
	"github.com/ipni/go-libipni/announce/httpsender"
	"github.com/ipni/go-libipni/dagsync"
	"github.com/ipni/go-libipni/dagsync/ipnisync"
	"github.com/ipni/go-libipni/ingest/schema"
	"github.com/ipni/go-naam"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multicodec"
	"github.com/multiformats/go-multihash"
	"github.com/multiformats/go-varint"
	"github.com/stretchr/testify/require"
)

const (
	announceURL = "http://localhost:3001"  // indexer ingest URL
	findURL     = "http://localhost:40080" // dhstore service URL
	listenAddr  = "127.0.0.1:9876"
	// multiaddr telling the indexer where to fetch advertisements from.
	publisherAddr = "/ip4/127.0.0.1/tcp/9876/http"
	// multiaddr to use as provider address in anvertisements.
	providerAddr = "/dns4/ipfs.io/tcp/443/https"

	indexerReadyMatch = "Indexer is ready"
	dhstoreReady      = "Store opened."
)

func TestNaam(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	te := setupTestEnv(t, ctx)
	defer te.Stop()

	someCid, err := cid.Decode("bafzaajaiaejcbzdibmxyzdjbbehgvizh6g5tikvy47mshdy6gwbruvgwvd24seje")
	require.NoError(t, err)

	h, err := libp2p.New()
	require.NoError(t, err)

	n, err := naam.New(naam.WithHost(h),
		naam.WithListenAddr(listenAddr),
		naam.WithAnnounceURL(announceURL),
		naam.WithFindURL(findURL),
		naam.WithPublisherAddrs(publisherAddr),
		naam.WithProviderAddrs(providerAddr),
	)
	require.NoError(t, err)

	// Publish IPNS record to IPNI indexer.
	publishedPath := path.FromCid(someCid)
	err = n.Publish(ctx, publishedPath, naam.WithEOL(time.Now().Add(48*time.Hour)))
	require.NoError(t, err)
	ipnsName := n.Name()
	t.Log("IPNS record published with name", ipnsName)

	// Resolve locally - avoids indexer lookup if naam instance is the publisher.
	resolvedPath, err := n.Resolve(ctx, ipnsName)
	require.NoError(t, err)
	t.Logf("Resolved IPNS record locally:\n %s ==> %s", ipnsName, resolvedPath)

	// Resolve by looking up IPNS record using indexer.
	require.Eventually(t, func() bool {
		resolvedPath, err = naam.ResolveNotPrivate(ctx, ipnsName, findURL)
		return err == nil
	}, 10*time.Second, time.Second)
	t.Logf("⚠️  Reader privacy disabled | Resolved IPNS record using indexer:\n %s ==> %s", ipnsName, resolvedPath)

	// Resolve by looking up IPNS record using indexer with reader-privacy.
	resolvedPath, err = naam.Resolve(ctx, ipnsName, findURL)
	require.NoError(t, err)
	t.Logf("🔒 Reader privacy enabled | Resolved IPNS record using indexer:\n %s ==> %s", ipnsName, resolvedPath)

	// Resolve a name that does not have an IPNS record.
	pid, err := peer.Decode("12D3KooWPbQ26UtFJ48ybpCyUoFYFBqH64DbHGMAKtXobKtRdzFF")
	require.NoError(t, err)
	anotherName := naam.Name(pid)
	resolvedPath, err = naam.Resolve(ctx, anotherName, findURL)
	require.ErrorIs(t, err, naam.ErrNotFound)
	t.Log("Record for unknown name", anotherName, "not found, as expected")

	t.Log("Attacker publishes alternate IPNS record with existing name to try to hijack IPNS")
	err = hijackPublishNaam(ctx, ipnsName)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		resolvedPath, err = naam.ResolveNotPrivate(ctx, ipnsName, findURL)
		return err != nil || resolvedPath.String() != "/ipfs/bafzaajaiaejcbzdibmxyzdjbbehgvizh6g5tikvy47mshdy6gwbruvgwvd24seje"
	}, 10*time.Second, time.Second)
	require.Error(t, err)
}

func hijackPublishNaam(ctx context.Context, ipnsName string) error {
	hackCid, err := cid.Decode("bafybeigvgzoolc3drupxhlevdp2ugqcrbcsqfmcek2zxiw5wctk3xjpjwy")
	if err != nil {
		return err
	}

	hackNaam, err := newHackNaam("127.0.0.1:9080", announceURL)
	if err != nil {
		return err
	}

	// Path of CID that hijacked IPNS name resolves to.
	publishedPath := path.FromCid(hackCid)

	// Publish IPNS record to IPNI indexer.
	err = hackNaam.publish(ctx, publishedPath, ipnsName)
	if err != nil {
		return fmt.Errorf("failed to publish ipns record to ipni: %s", err)
	}
	return nil
}

type testEnv struct {
	e          *testcmd.Runner
	cmdIndexer *exec.Cmd
	cmdDhstore *exec.Cmd
}

func (te testEnv) Stop() {
	te.e.Stop(te.cmdIndexer, 2*time.Second)
	te.e.Stop(te.cmdDhstore, 2*time.Second)
}

func setupTestEnv(t *testing.T, ctx context.Context) testEnv {
	e := testcmd.NewRunner(t, t.TempDir())

	indexer := filepath.Join(e.Dir, "storetheindex")
	dhstore := filepath.Join(e.Dir, "dhstore")

	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(e.Dir))

	// install storetheindex
	t.Log("installing storetheindex into test environment")
	e.Run(ctx, "go", "install", "github.com/ipni/storetheindex@latest")
	// install dhstore
	t.Log("installing dhstore into test environment")
	e.Run(ctx, "go", "install", "-tags", "nofdb", "github.com/ipni/dhstore/cmd/dhstore@latest")

	require.NoError(t, os.Chdir(cwd))

	// initialize indexer
	e.Run(ctx, indexer, "init", "--pubsub-topic", "/indexer/ingest/testnet", "--no-bootstrap",
		"--store", "dhstore", "--dhstore", "http://127.0.0.1:40080")

	// start dhstore
	t.Log("starting dhstore")
	dhstoreReady := testcmd.NewStderrWatcher(dhstoreReady)
	cmdDhstore := e.Start(ctx, testcmd.Args(dhstore, "-storePath", e.Dir, "-providersURL", "http://localhost:3000"), dhstoreReady)
	err = dhstoreReady.Wait(ctx)

	require.NoError(t, err, "timed out waiting for dhstore to start")

	// start indexer
	t.Log("starting storetheindex")
	indexerReady := testcmd.NewStdoutWatcher(indexerReadyMatch)
	cmdIndexer := e.Start(ctx, testcmd.Args(indexer, "daemon"), indexerReady)
	err = indexerReady.Wait(ctx)
	require.NoError(t, err, "timed out waiting for indexer to start")

	return testEnv{
		e:          e,
		cmdIndexer: cmdIndexer,
		cmdDhstore: cmdDhstore,
	}
}

var (
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

type hackNaam struct {
	ds            datastore.Datastore
	h             host.Host
	httpAnnouncer *httpsender.Sender
	ls            *ipld.LinkSystem
	pub           dagsync.Publisher
}

func newHackNaam(httpListenAddr, httpIndexerURL string) (*hackNaam, error) {
	h, err := libp2p.New()
	if err != nil {
		return nil, err
	}

	ds := sync.MutexWrap(datastore.NewMapDatastore())
	linkSys := makeLinkSys(ds)

	// Create publisher that publishes over http and libptp.
	pk := h.Peerstore().PrivKey(h.ID())
	pub, err := ipnisync.NewPublisher(*linkSys, pk,
		ipnisync.WithHTTPListenAddrs(httpListenAddr),
		ipnisync.WithStreamHost(h),
	)
	if err != nil {
		return nil, err
	}

	indexerURL, err := url.Parse(httpIndexerURL)
	if err != nil {
		return nil, err
	}

	// Create a direct http announcement sender.
	httpAnnouncer, err := httpsender.New([]*url.URL{indexerURL}, h.ID())
	if err != nil {
		return nil, err
	}

	return &hackNaam{
		ds:            ds,
		h:             h,
		httpAnnouncer: httpAnnouncer,
		ls:            linkSys,
		pub:           pub,
	}, nil
}

func makeLinkSys(ds datastore.Datastore) *ipld.LinkSystem {
	linkSys := cidlink.DefaultLinkSystem()
	ds = namespace.Wrap(ds, datastore.NewKey("ls"))
	linkSys.StorageReadOpener = func(ctx linking.LinkContext, l datamodel.Link) (io.Reader, error) {
		val, err := ds.Get(ctx.Ctx, datastore.NewKey(l.Binary()))
		if err != nil {
			return nil, err
		}
		return bytes.NewBuffer(val), nil
	}
	linkSys.StorageWriteOpener = func(ctx ipld.LinkContext) (io.Writer, ipld.BlockWriteCommitter, error) {
		buf := bytes.NewBuffer(nil)
		return buf, func(l ipld.Link) error {
			return ds.Put(ctx.Ctx, datastore.NewKey(l.Binary()), buf.Bytes())
		}, nil
	}
	return &linkSys
}

func peerIDFromName(name string) (peer.ID, error) {
	spid := strings.TrimPrefix(name, ipns.NamespacePrefix)
	if spid == name {
		// Missing `/ipns/` prefix.
		return "", ipns.ErrInvalidName
	}
	return peer.Decode(spid)
}

func (n *hackNaam) publish(ctx context.Context, value path.Path, origName string) error {
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

	eol := time.Now().Add(24 * time.Hour)
	var ttl time.Duration
	ipnsRec, err := ipns.NewRecord(pk, value, seq, eol, ttl, ipns.WithPublicKey(true))
	if err != nil {
		return err
	}

	hackPID, err := peerIDFromName(origName)
	if err != nil {
		return err
	}

	// Store entry block.
	mh, err := multihash.Sum([]byte(hackPID), multihash.SHA2_256, -1)
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
		Provider:   hackPID.String(),
		Addresses:  n.adAddrs(),
		Entries:    entriesLink,
		ContextID:  naam.ContextID,
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

func (n *hackNaam) adAddrs() []string {
	pa := n.pub.Addrs()
	adAddrs := make([]string, 0, len(pa))
	for _, a := range pa {
		adAddrs = append(adAddrs, a.String())
	}
	return adAddrs
}

func (n *hackNaam) setHeadAdCid(ctx context.Context, head cid.Cid) error {
	if err := n.ds.Put(ctx, headAdCid, head.Bytes()); err != nil {
		return err
	}
	h, err := n.previousHeight(ctx)
	if err != nil {
		return err
	}
	return n.ds.Put(ctx, height, varint.ToUvarint(h+1))
}

func (n *hackNaam) previousHeight(ctx context.Context) (uint64, error) {
	v, err := n.ds.Get(ctx, height)
	if err != nil {
		if errors.Is(err, datastore.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	buf := bytes.NewBuffer(v)
	return varint.ReadUvarint(buf)
}

func (n *hackNaam) getHeadAdCid(ctx context.Context) (cid.Cid, error) {
	c, err := n.ds.Get(ctx, headAdCid)
	if err != nil {
		if errors.Is(err, datastore.ErrNotFound) {
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
	metadata.Write(varint.ToUvarint(uint64(naam.MetadataProtocolID)))
	marshal, err := ipns.MarshalRecord(rec)
	if err != nil {
		return nil, err
	}
	metadata.Write(marshal)
	return metadata.Bytes(), nil
}
