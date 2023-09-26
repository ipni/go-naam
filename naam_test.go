package naam_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ipfs/boxo/path"
	"github.com/ipfs/go-cid"
	"github.com/ipni/go-naam"
	stitest "github.com/ipni/storetheindex/test"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"
)

func TestNaam(t *testing.T) {
	const (
		announceURL = "http://localhost:3001"  // indexer ingest URL
		findURL     = "http://localhost:40080" // dhstore service URL
	)

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	te := setupTestEnv(t, ctx)

	someCid, err := cid.Decode("bafzaajaiaejcbzdibmxyzdjbbehgvizh6g5tikvy47mshdy6gwbruvgwvd24seje")
	require.NoError(t, err)

	h, err := libp2p.New()
	require.NoError(t, err)

	n, err := naam.New(naam.WithHost(h),
		naam.WithHttpListenAddr("127.0.0.1"),
		naam.WithHttpIndexerURL(announceURL),
		naam.WithHttpFindURL(findURL),
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

	te.Stop()
}

type testEnv struct {
	e          *stitest.TestIpniRunner
	cmdIndexer *exec.Cmd
	cmdDhstore *exec.Cmd
}

func (te testEnv) Stop() {
	te.e.Stop(te.cmdIndexer, 2*time.Second)
	te.e.Stop(te.cmdDhstore, 2*time.Second)
}

func setupTestEnv(t *testing.T, ctx context.Context) testEnv {
	e := stitest.NewTestIpniRunner(t, ctx, t.TempDir())

	indexer := filepath.Join(e.Dir, "storetheindex")
	dhstore := filepath.Join(e.Dir, "dhstore")

	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(e.Dir))

	// install storetheindex
	e.Run("go", "install", "github.com/ipni/storetheindex@latest")
	// install dhstore
	e.Run("go", "install", "-tags", "nofdb", "github.com/ipni/dhstore/cmd/dhstore@latest")

	require.NoError(t, os.Chdir(cwd))

	// initialize indexer
	e.Run(indexer, "init", "--pubsub-topic", "/indexer/ingest/testnet", "--no-bootstrap",
		"--store", "dhstore", "--dhstore", "http://127.0.0.1:40080")

	// start dhstore
	dhstoreReady := stitest.NewStdoutWatcher(stitest.DhstoreReady)
	cmdDhstore := e.Start(stitest.NewExecution(dhstore, "-storePath", e.Dir, "-providersURL", "http://localhost:3000").WithWatcher(dhstoreReady))
	select {
	case <-dhstoreReady.Signal:
	case <-ctx.Done():
		t.Fatal("timed out waiting for dhstore to start")
	}

	// start indexer
	indexerReady := stitest.NewStdoutWatcher(stitest.IndexerReadyMatch)
	cmdIndexer := e.Start(stitest.NewExecution(indexer, "daemon").WithWatcher(indexerReady))
	select {
	case <-indexerReady.Signal:
	case <-ctx.Done():
		t.Fatal("timed out waiting for indexer to start")
	}

	return testEnv{
		e:          e,
		cmdIndexer: cmdIndexer,
		cmdDhstore: cmdDhstore,
	}
}
