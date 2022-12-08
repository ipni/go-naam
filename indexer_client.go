package naam

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/fxamacker/cbor/v2"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
	"github.com/multiformats/go-multiaddr"
	"github.com/multiformats/go-multihash"
)

type (
	announcement struct {
		Cid   cid.Cid
		Addrs []multiaddr.Multiaddr
	}
	lookupResult struct {
		MultihashResults []struct {
			Multihash       multihash.Multihash `json:"Multihash"`
			ProviderResults []struct {
				ContextID []byte        `json:"ContextID"`
				Metadata  []byte        `json:"Metadata"`
				Provider  peer.AddrInfo `json:"Provider"`
			} `json:"ProviderResults"`
		} `json:"MultihashResults"`
	}
)

func (a announcement) MarshalCBOR() ([]byte, error) {
	msg := make([]any, 0, 3)
	msg = append(msg, cbor.Tag{
		Number:  42,
		Content: append([]byte{0}, a.Cid.Bytes()...),
	})
	baddrs := make([][]byte, 0, len(a.Addrs))
	for _, addr := range a.Addrs {
		baddrs = append(baddrs, addr.Bytes())
	}
	msg = append(msg, baddrs)
	msg = append(msg, []byte{0})
	return cbor.Marshal(msg)
}

func (n *Naam) announce(ctx context.Context, ai *peer.AddrInfo, head cid.Cid) error {
	maddrs, err := peer.AddrInfoToP2pAddrs(ai)
	if err != nil {
		return err
	}
	anncb, err := cbor.Marshal(announcement{
		Cid:   head,
		Addrs: maddrs,
	})
	if err != nil {
		return err
	}
	url := n.httpIndexerEndpoint + `/ingest/announce`
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewBuffer(anncb))
	if err != nil {
		return err
	}
	resp, err := n.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respb, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode > 299 {
		return fmt.Errorf("unsuccessful announce: %d %s", resp.StatusCode, string(respb))
	}
	return nil
}

func (n *Naam) getNaamMetadata(ctx context.Context, mh multihash.Multihash) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.httpIndexerEndpoint+"/multihash/"+mh.B58String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := n.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unsuccessful lookup: %d %s", resp.StatusCode, string(body))
	}
	var r lookupResult
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	for _, mhr := range r.MultihashResults {
		if bytes.Equal(mhr.Multihash, mh) {
			for _, pr := range mhr.ProviderResults {
				if bytes.Equal(pr.ContextID, ContextID) {
					return pr.Metadata, nil
				}
			}
		}
	}
	return nil, routing.ErrNotFound
}
