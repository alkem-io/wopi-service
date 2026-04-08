// Package collabora implements the Collabora Online discovery client adapter.
package collabora

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/alkem-io/wopi-service/internal/domain/port"
)

// DiscoveryClient fetches WOPI discovery data from Collabora Online.
type DiscoveryClient struct {
	collaboraURL string
	httpClient   *http.Client
}

// NewDiscoveryClient creates a new DiscoveryClient.
func NewDiscoveryClient(collaboraURL string) *DiscoveryClient {
	return &DiscoveryClient{
		collaboraURL: collaboraURL,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
	}
}

type wopiDiscovery struct {
	XMLName  xml.Name  `xml:"wopi-discovery"`
	NetZones []netZone `xml:"net-zone"`
	ProofKey proofKey  `xml:"proof-key"`
}

type netZone struct {
	Name string `xml:"name,attr"`
	Apps []app  `xml:"app"`
}

type app struct {
	Name       string   `xml:"name,attr"`
	FavIconURL string   `xml:"favIconUrl,attr"`
	Actions    []action `xml:"action"`
}

type action struct {
	Name     string `xml:"name,attr"`
	Ext      string `xml:"ext,attr"`
	URLSrc   string `xml:"urlsrc,attr"`
	Requires string `xml:"requires,attr"`
}

type proofKey struct {
	Modulus     string `xml:"modulus,attr"`
	Exponent    string `xml:"exponent,attr"`
	OldModulus  string `xml:"oldmodulus,attr"`
	OldExponent string `xml:"oldexponent,attr"`
}

// FetchDiscovery retrieves and parses the Collabora discovery XML.
func (c *DiscoveryClient) FetchDiscovery(ctx context.Context) (*port.DiscoveryData, error) {
	url := fmt.Sprintf("%s/hosting/discovery", c.collaboraURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create discovery request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch discovery: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read discovery body: %w", err)
	}

	var discovery wopiDiscovery
	if err := xml.Unmarshal(body, &discovery); err != nil {
		return nil, fmt.Errorf("parse discovery XML: %w", err)
	}

	var actions []port.DiscoveryAction
	for _, zone := range discovery.NetZones {
		for _, a := range zone.Apps {
			for _, act := range a.Actions {
				actions = append(actions, port.DiscoveryAction{
					App:    a.Name,
					Name:   act.Name,
					Ext:    act.Ext,
					URLSrc: act.URLSrc,
				})
			}
		}
	}

	return &port.DiscoveryData{
		Actions: actions,
		ProofKey: port.ProofKey{
			Modulus:     discovery.ProofKey.Modulus,
			Exponent:    discovery.ProofKey.Exponent,
			OldModulus:  discovery.ProofKey.OldModulus,
			OldExponent: discovery.ProofKey.OldExponent,
		},
	}, nil
}
