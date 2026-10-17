package storagecap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/sync/errgroup"
)

// maxStatusBytes bounds a status document read from the cluster.
const maxStatusBytes = 4 << 20

// maxReportedBytes is the largest byte count kept from an unsigned report; a
// larger one is clamped to it.
const maxReportedBytes = 1 << 62

// ClusterCapacity is the disk the cluster's volume servers report: every disk
// directory of every server read, summed.
type ClusterCapacity struct {
	Total int64 `json:"total_bytes"`
	Used  int64 `json:"used_bytes"`
	Free  int64 `json:"free_bytes"`
	// Servers is how many volume servers answered; Unreached lists the ones
	// that did not, so a partial reading is labeled as one.
	Servers   int      `json:"servers"`
	Unreached []string `json:"unreached"`
}

// dirStatus is the part of the master's /dir/status read: each data node's
// address.
type dirStatus struct {
	Topology struct {
		DataCenters []struct {
			Racks []struct {
				DataNodes []dataNode `json:"DataNodes"`
			} `json:"Racks"`
		} `json:"DataCenters"`
	} `json:"Topology"`
}

// dataNode is one data node of the master's topology.
type dataNode struct {
	URL       string `json:"Url"`
	PublicURL string `json:"PublicUrl"`
}

// address is the node's public address, else its address; empty when it
// reports neither.
func (n dataNode) address() string {
	if n.PublicURL != "" {
		return n.PublicURL
	}
	return n.URL
}

// nodes is every data node of every rack of every data center.
func (st dirStatus) nodes() []dataNode {
	var out []dataNode
	for _, dc := range st.Topology.DataCenters {
		for _, rack := range dc.Racks {
			out = append(out, rack.DataNodes...)
		}
	}
	return out
}

// volumeStatus is the part of a volume server's /status read: its disks.
type volumeStatus struct {
	DiskStatuses []struct {
		Dir  string `json:"dir"`
		All  uint64 `json:"all"`
		Used uint64 `json:"used"`
		Free uint64 `json:"free"`
	} `json:"DiskStatuses"`
}

// errNoServers is the reading of a cluster none of whose volume servers
// answered.
var errNoServers = errors.New("no SeaweedFS volume server answered")

// ReadSeaweedFS reads the cluster's disk capacity: the volume servers cfg
// names, or the ones the master lists, each asked for its disks.
func ReadSeaweedFS(ctx context.Context, client *http.Client, cfg SeaweedFS) (ClusterCapacity, error) {
	servers := cfg.VolumeURLs
	if len(servers) == 0 {
		listed, err := listVolumeServers(ctx, client, cfg.MasterURL)
		if err != nil {
			return ClusterCapacity{}, err
		}
		servers = listed
	}
	// Every server is asked at once, so the read waits on the slowest one
	// rather than on the sum of them.
	statuses := make([]*volumeStatus, len(servers))
	var g errgroup.Group
	for i, server := range servers {
		g.Go(func() error {
			var st volumeStatus
			if err := getJSON(ctx, client, server+"/status", &st); err == nil {
				statuses[i] = &st
			}
			return nil
		})
	}
	_ = g.Wait()
	out := ClusterCapacity{Unreached: []string{}}
	for i, st := range statuses {
		if st == nil {
			out.Unreached = append(out.Unreached, servers[i])
			continue
		}
		out.Servers++
		for _, d := range st.DiskStatuses {
			out.Total += clamp(d.All)
			out.Used += clamp(d.Used)
			out.Free += clamp(d.Free)
		}
	}
	if out.Servers == 0 {
		return out, fmt.Errorf("reading volume servers %s: %w", strings.Join(out.Unreached, ", "), errNoServers)
	}
	return out, nil
}

// listVolumeServers reads the master's topology for its data nodes.
func listVolumeServers(ctx context.Context, client *http.Client, master string) ([]string, error) {
	var st dirStatus
	if err := getJSON(ctx, client, master+"/dir/status", &st); err != nil {
		return nil, fmt.Errorf("reading the SeaweedFS master: %w", err)
	}
	seen := map[string]bool{}
	var out []string
	for _, node := range st.nodes() {
		addr := node.address()
		if addr == "" || seen[addr] {
			continue
		}
		seen[addr] = true
		if !strings.Contains(addr, "://") {
			addr = "http://" + addr
		}
		out = append(out, addr)
	}
	if len(out) == 0 {
		return nil, errors.New("the SeaweedFS master lists no volume server")
	}
	return out, nil
}

// getJSON reads one JSON document.
func getJSON(ctx context.Context, client *http.Client, address string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, http.NoBody)
	if err != nil {
		return fmt.Errorf("building the request: %w", err)
	}
	resp, err := client.Do(req) // #nosec G704 -- the operator's own cluster address, validated as http(s) on save
	if err != nil {
		return fmt.Errorf("reading %s: %w", address, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("reading %s: status %d", address, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxStatusBytes)).Decode(into); err != nil {
		return fmt.Errorf("decoding %s: %w", address, err)
	}
	return nil
}

// clamp is a byte count reported unsigned, as the int64 every other figure is.
func clamp(v uint64) int64 {
	if v > maxReportedBytes {
		return maxReportedBytes
	}
	return int64(v)
}
