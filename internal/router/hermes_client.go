package router

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// hermesUpdateResponse is the JSON body returned by the Pyth Hermes v2
// /v2/updates/price/latest endpoint.
type hermesUpdateResponse struct {
	Binary struct {
		Encoding string   `json:"encoding"`
		Data     []string `json:"data"`
	} `json:"binary"`
}

// HermesClient fetches live price update VAAs from the authenticated Pyth Hermes
// endpoint and decodes the active router set index from the PNAU binary.
type HermesClient struct {
	baseURL     string
	apiKey      string
	priceFeedID string
	client      *http.Client
}

func NewHermesClient(baseURL, apiKey, priceFeedID string) *HermesClient {
	return &HermesClient{
		baseURL:     baseURL,
		apiKey:      apiKey,
		priceFeedID: priceFeedID,
		client:      &http.Client{Timeout: 15 * time.Second},
	}
}

// GetCurrentRouterSetIndex fetches a live price update VAA from the Hermes endpoint
// and extracts the router set index that signed it. This is the authoritative current
// index — analogous to the Ethereum Wormhole contract in the old architecture.
func (c *HermesClient) GetCurrentRouterSetIndex(ctx context.Context) (uint32, error) {
	feedID := strings.TrimPrefix(c.priceFeedID, "0x")
	url := fmt.Sprintf("%s/v2/updates/price/latest?ids[]=%s&encoding=base64", c.baseURL, feedID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetch hermes update: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("hermes API returned status %d", resp.StatusCode)
	}

	var result hermesUpdateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode hermes response: %w", err)
	}
	if len(result.Binary.Data) == 0 {
		return 0, fmt.Errorf("hermes response contains no binary data")
	}

	pnau, err := base64.StdEncoding.DecodeString(result.Binary.Data[0])
	if err != nil {
		return 0, fmt.Errorf("decode PNAU base64: %w", err)
	}

	return decodeRouterSetIndex(pnau)
}

// decodeRouterSetIndex extracts the router set index from a PNAU
// (Price Network Accumulator Update) binary.
//
// New PNAU layout (Pyth router model):
//
//	bytes  0-9  : 10-byte PNAU header
//	byte   10   : embedded VAA version
//	bytes 11-14 : router set index (= vaa[1:5] where vaa starts at byte 10)
//	bytes 15+   : remainder of embedded VAA
func decodeRouterSetIndex(pnau []byte) (uint32, error) {
	// Minimum: 10-byte header + 1-byte version + 4-byte index = 15 bytes
	if len(pnau) < 15 {
		return 0, fmt.Errorf("PNAU too short: %d bytes (need at least 15)", len(pnau))
	}
	return binary.BigEndian.Uint32(pnau[11:15]), nil
}
