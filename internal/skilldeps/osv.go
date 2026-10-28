package skilldeps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Finding is one resolved package OSV lists as malicious.
type Finding struct {
	Package Package
	ID      string
	Summary string
}

// MalwareChecker reports which of pkgs are known to be malicious.
type MalwareChecker interface {
	Malicious(ctx context.Context, pkgs []Package) ([]Finding, error)
}

// OSVClient asks OSV (osv.dev), whose PyPI feed carries the OpenSSF malicious-packages
// advisories under MAL- ids. Only those count here: an ordinary vulnerability (GHSA-,
// PYSEC-) is a reason to upgrade, not evidence that the package itself is hostile.
type OSVClient struct {
	baseURL string
	hc      *http.Client
}

// osvBatchLimit is OSV's maximum number of queries per querybatch request.
const osvBatchLimit = 1000

// NewOSVClient returns a client for baseURL ("" → https://api.osv.dev). A nil hc gets a
// client with a 20s timeout.
func NewOSVClient(baseURL string, hc *http.Client) *OSVClient {
	if baseURL == "" {
		baseURL = "https://api.osv.dev"
	}
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	return &OSVClient{baseURL: strings.TrimRight(baseURL, "/"), hc: hc}
}

type osvQuery struct {
	Package struct {
		Name      string `json:"name"`
		Ecosystem string `json:"ecosystem"`
	} `json:"package"`
	Version string `json:"version"`
}

type osvBatchResponse struct {
	Results []struct {
		Vulns []struct {
			ID string `json:"id"`
		} `json:"vulns"`
	} `json:"results"`
}

// Malicious implements MalwareChecker.
func (c *OSVClient) Malicious(ctx context.Context, pkgs []Package) ([]Finding, error) {
	var out []Finding
	for start := 0; start < len(pkgs); start += osvBatchLimit {
		end := min(start+osvBatchLimit, len(pkgs))
		batch := pkgs[start:end]
		queries := make([]osvQuery, len(batch))
		for i, p := range batch {
			queries[i].Package.Name = p.Name
			queries[i].Package.Ecosystem = "PyPI"
			queries[i].Version = p.Version
		}
		body, _ := json.Marshal(map[string]any{"queries": queries})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/querybatch", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.hc.Do(req)
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("osv.dev answered %d", resp.StatusCode)
		}
		var parsed osvBatchResponse
		if err := json.Unmarshal(data, &parsed); err != nil {
			return nil, fmt.Errorf("osv.dev: %w", err)
		}
		for i, r := range parsed.Results {
			if i >= len(batch) {
				break
			}
			for _, v := range r.Vulns {
				if strings.HasPrefix(v.ID, "MAL-") {
					out = append(out, Finding{Package: batch[i], ID: v.ID})
				}
			}
		}
	}
	return out, nil
}
