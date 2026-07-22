package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/chainzero/akash-bme-monitor/internal/akashclient"
)

// getConfigQuery is the base64-encoded CosmWasm smart query {"get_config":{}}.
// Used to query the pyth-vaa contract for the current router set configuration.
const getConfigQuery = "eyJnZXRfY29uZmlnIjp7fX0="

// pythVAAConfigResponse is the JSON body returned by the pyth-vaa contract
// {"get_config":{}} smart query.
type pythVAAConfigResponse struct {
	Data struct {
		RouterVerifier struct {
			RouterSetIndex uint32 `json:"router_set_index"`
		} `json:"router_verifier"`
	} `json:"data"`
}

// PythVAAClient queries the Akash pyth-vaa CosmWasm contract for the router
// set configuration currently registered on-chain.
type PythVAAClient struct {
	apiNodes        []string
	network         string
	pythVAAContract string
	client          *http.Client
}

func NewPythVAAClient(apiNodes []string, network, pythVAAContract string) *PythVAAClient {
	return &PythVAAClient{
		apiNodes:        apiNodes,
		network:         network,
		pythVAAContract: pythVAAContract,
		client:          &http.Client{Timeout: 10 * time.Second},
	}
}

// GetRouterSetIndex returns the router_set_index currently stored in the
// pyth-vaa contract. This is what the contract will accept when verifying
// incoming price update VAAs — if it lags behind the live Hermes index,
// price submissions will fail with InvalidRouterSetIndex.
func (c *PythVAAClient) GetRouterSetIndex(ctx context.Context) (uint32, error) {
	if c.pythVAAContract == "" {
		return 0, fmt.Errorf("pyth_vaa_contract not configured for network %q", c.network)
	}

	path := fmt.Sprintf("/cosmwasm/wasm/v1/contract/%s/smart/%s",
		c.pythVAAContract, getConfigQuery)

	resp, err := akashclient.Fetch(ctx, c.client, c.apiNodes, path)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected status %d from pyth-vaa contract", resp.StatusCode)
	}

	var result pythVAAConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode get_config response: %w", err)
	}

	return result.Data.RouterVerifier.RouterSetIndex, nil
}
