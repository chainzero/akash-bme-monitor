package guardian

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/chainzero/akash-bme-monitor/internal/config"
)

const (
	wormholeMainnetContract   = "0x98f3c9e6E3fAce36bAAd05FE09d375Ef1464288B"
	wormholeGovernanceEmitter = "0000000000000000000000000000000000000000000000000000000000000004"

	// akashWormholeContract is the CosmWasm Wormhole contract address on Akash.
	// This address is deterministic and identical across mainnet, testnet, and sandbox.
	akashWormholeContract = "akash14hj2tavq8fpesdwxxcu44rty3hh90vhujrvcmstl4zr3txmfvw9sggdamt"
)

// RunVAAFetchTest exercises both VAA fetch paths with step-by-step diagnostic output.
// Returns 0 if at least one path produces a valid upgrade VAA, 1 if both fail.
func RunVAAFetchTest(ctx context.Context, etherscanAPIKey string, targetIndex uint32, w io.Writer) int {
	signingIndex := targetIndex - 1
	fmt.Fprintf(w, "\n=== VAA Fetch Test: Guardian Set %d → %d ===\n\n", signingIndex, targetIndex)

	ethVAA, ethTX := runEtherscanDiag(ctx, etherscanAPIKey, targetIndex, w)
	fmt.Fprintln(w)
	wormVAA := runWormholescanDiag(ctx, targetIndex, w)

	fmt.Fprintf(w, "\n=== Summary ===\n")
	if ethVAA != "" {
		fmt.Fprintf(w, "  Etherscan path:    PASS ✅\n")
	} else {
		fmt.Fprintf(w, "  Etherscan path:    FAIL ❌\n")
	}
	if wormVAA != "" {
		fmt.Fprintf(w, "  Wormholescan path: PASS ✅\n")
	} else {
		fmt.Fprintf(w, "  Wormholescan path: FAIL ❌\n")
	}

	bestVAA, source := ethVAA, "Etherscan"
	if bestVAA == "" {
		bestVAA, source = wormVAA, "Wormholescan"
	}

	if bestVAA != "" {
		fmt.Fprintf(w, "  VAA to use:        %s (from %s)\n", vaaPrefix(bestVAA), source)
		if ethTX != "" {
			fmt.Fprintf(w, "  Source TX:         %s\n", ethTX)
		}
		printSlackPreview(w, targetIndex, ethTX, bestVAA)
		return 0
	}

	fmt.Fprintf(w, "\nBoth paths failed — retrieve the VAA manually:\n")
	fmt.Fprintf(w, "  curl -s \"https://api.wormholescan.io/api/v1/vaas/1/%s?pageSize=100\" | jq '.'\n",
		wormholeGovernanceEmitter)
	return 1
}

// printSlackPreview renders the rotation alert body exactly as the monitoring app
// would send it to Slack, using placeholder values for the network fields that
// normally come from config.yaml.
func printSlackPreview(w io.Writer, targetIndex uint32, ethTxHash, vaaBase64 string) {
	mockNetwork := config.NetworkConfig{
		Name:             "<network-name>",
		WormholeContract: akashWormholeContract,
		OperatorAddress:  "<operator-wallet>",
		ChainID:          "<chain-id>",
	}

	var body strings.Builder
	fmt.Fprintf(&body,
		"The Wormhole mainnet guardian set has rotated.\n\n"+
			"Previous Index: %d\n"+
			"New Index:      %d\n\n",
		targetIndex-1, targetIndex,
	)
	if ethTxHash != "" {
		fmt.Fprintf(&body, "Source TX: %s\n\n", ethTxHash)
	}
	fmt.Fprintf(&body,
		"PRICE FEED IS DOWN. Submit the VAA immediately for each network.\n\n"+
			"NOTE: There is NO grace period for live price feeds — prices stopped\n"+
			"the moment the rotation went live. Act immediately.\n\n",
	)
	fmt.Fprintf(&body, "--- %s ---\n", strings.ToUpper(mockNetwork.Name))
	fmt.Fprint(&body, buildSubmitCommand(mockNetwork, vaaBase64))

	border := strings.Repeat("─", 72)
	fmt.Fprintf(w, "\n--- Slack Alert Preview (rotation) ---\n")
	fmt.Fprintf(w, "Title: GUARDIAN SET ROTATION: INDEX %d → %d — PRICE FEED DOWN\n", targetIndex-1, targetIndex)
	fmt.Fprintf(w, "%s\n", border)
	fmt.Fprint(w, body.String())
	fmt.Fprintf(w, "%s\n", border)
}

func runEtherscanDiag(ctx context.Context, apiKey string, targetIndex uint32, w io.Writer) (vaaBase64, txHash string) {
	fmt.Fprintf(w, "--- Etherscan Path ---\n")
	fmt.Fprintf(w, "  Searching for function selector: %s\n", submitNewGuardianSetSelector)

	if apiKey == "" {
		fmt.Fprintf(w, "  SKIP: ETHERSCAN_API_KEY not set\n")
		fmt.Fprintf(w, "  Result: SKIP ⚠️\n")
		return "", ""
	}

	url := fmt.Sprintf(
		"https://api.etherscan.io/v2/api?chainid=1&module=account&action=txlist"+
			"&address=%s&sort=desc&page=1&offset=500&apikey=%s",
		wormholeMainnetContract, apiKey,
	)
	fmt.Fprintf(w, "  Fetching last 500 transactions to %s...\n", wormholeMainnetContract)

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintf(w, "  Error building request: %v\n", err)
		fmt.Fprintf(w, "  Result: FAIL ❌\n")
		return "", ""
	}

	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(w, "  Error fetching transactions: %v\n", err)
		fmt.Fprintf(w, "  Result: FAIL ❌\n")
		return "", ""
	}
	defer resp.Body.Close()

	var result etherscanTxListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Fprintf(w, "  Error decoding response: %v\n", err)
		fmt.Fprintf(w, "  Result: FAIL ❌\n")
		return "", ""
	}
	if result.Status != "1" {
		fmt.Fprintf(w, "  Etherscan API error: %s\n", result.Message)
		fmt.Fprintf(w, "  Result: FAIL ❌\n")
		return "", ""
	}

	fmt.Fprintf(w, "  Got %d transactions\n", len(result.Result))

	// Summarise unique function selectors present so we can diagnose selector mismatches.
	selectorCounts := make(map[string]int)
	for _, tx := range result.Result {
		input := strings.TrimPrefix(strings.ToLower(tx.Input), "0x")
		if len(input) >= 8 {
			key := input[:8]
			if tx.FunctionName != "" {
				key += " (" + tx.FunctionName + ")"
			}
			selectorCounts[key]++
		}
	}
	fmt.Fprintf(w, "  Unique selectors in these transactions:\n")
	for sel, count := range selectorCounts {
		marker := ""
		if strings.HasPrefix(sel, submitNewGuardianSetSelector) {
			marker = " ← target"
		}
		fmt.Fprintf(w, "    %s  ×%d%s\n", sel, count, marker)
	}

	signingIndex := targetIndex - 1
	selectorMatches := 0

	for _, tx := range result.Result {
		if tx.IsError == "1" {
			continue
		}
		input := strings.TrimPrefix(strings.ToLower(tx.Input), "0x")
		if len(input) < 8 || input[:8] != submitNewGuardianSetSelector {
			continue
		}
		selectorMatches++
		fmt.Fprintf(w, "  TX %s: submitNewGuardianSet call\n", tx.Hash)

		vaaBytes, err := decodeVAAFromCalldata(input)
		if err != nil {
			fmt.Fprintf(w, "    Decode error: %v\n", err)
			continue
		}
		fmt.Fprintf(w, "    VAA length: %d bytes\n", len(vaaBytes))

		if len(vaaBytes) < 5 {
			fmt.Fprintf(w, "    Error: VAA too short\n")
			continue
		}
		vaaSigningIdx := binary.BigEndian.Uint32(vaaBytes[1:5])
		if vaaSigningIdx != signingIndex {
			fmt.Fprintf(w, "    Signing index: %d (expected %d) — skipping\n", vaaSigningIdx, signingIndex)
			continue
		}
		fmt.Fprintf(w, "    Signing guardian set index: %d ✅ (expected: %d)\n", vaaSigningIdx, signingIndex)

		if err := validateGuardianSetUpgradeVAA(vaaBytes, targetIndex); err != nil {
			fmt.Fprintf(w, "    Validation failed: %v\n", err)
			continue
		}

		numSigs := int(vaaBytes[5])
		payload := vaaBytes[6+numSigs*66+51:]
		fmt.Fprintf(w, "    Action type: %d (Core guardian set upgrade) ✅\n", payload[32])
		fmt.Fprintf(w, "    Target guardian set index: %d ✅ (expected: %d)\n",
			binary.BigEndian.Uint32(payload[35:39]), targetIndex)

		encoded := base64.StdEncoding.EncodeToString(vaaBytes)
		fmt.Fprintf(w, "    VAA prefix: %s\n", vaaPrefix(encoded))
		fmt.Fprintf(w, "  Result: PASS ✅\n")
		return encoded, tx.Hash
	}

	if selectorMatches == 0 {
		fmt.Fprintf(w, "  No submitNewGuardianSet transactions found in last 50\n")
	}
	fmt.Fprintf(w, "  Result: FAIL ❌\n")
	return "", ""
}

func runWormholescanDiag(ctx context.Context, targetIndex uint32, w io.Writer) (vaaBase64 string) {
	fmt.Fprintf(w, "--- Wormholescan Path ---\n")

	url := fmt.Sprintf("https://api.wormholescan.io/api/v1/vaas/1/%s?pageSize=100", wormholeGovernanceEmitter)
	fmt.Fprintf(w, "  Querying emitter 0x%s...%s, pageSize=20...\n",
		wormholeGovernanceEmitter[:4], wormholeGovernanceEmitter[60:])

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintf(w, "  Error building request: %v\n", err)
		fmt.Fprintf(w, "  Result: FAIL ❌\n")
		return ""
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(w, "  Error fetching VAAs: %v\n", err)
		fmt.Fprintf(w, "  Result: FAIL ❌\n")
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(w, "  HTTP %d\n", resp.StatusCode)
		fmt.Fprintf(w, "  Result: FAIL ❌\n")
		return ""
	}

	var result vaaListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Fprintf(w, "  Error decoding response: %v\n", err)
		fmt.Fprintf(w, "  Result: FAIL ❌\n")
		return ""
	}

	fmt.Fprintf(w, "  Scanned %d entries\n", len(result.Data))
	signingIndex := targetIndex - 1

	for i, entry := range result.Data {
		vaaBytes, err := base64.StdEncoding.DecodeString(entry.VAA)
		if err != nil {
			fmt.Fprintf(w, "  Entry %2d: signing_index=%d — base64 decode error: %v\n",
				i, entry.GuardianSetIndex, err)
			continue
		}

		actionDesc := extractActionDesc(vaaBytes)

		if entry.GuardianSetIndex != signingIndex {
			fmt.Fprintf(w, "  Entry %2d: signing_index=%d, action_type=%s — wrong signing index, skipping\n",
				i, entry.GuardianSetIndex, actionDesc)
			continue
		}

		if err := validateGuardianSetUpgradeVAA(vaaBytes, targetIndex); err != nil {
			fmt.Fprintf(w, "  Entry %2d: signing_index=%d, action_type=%s — validation failed: %v\n",
				i, entry.GuardianSetIndex, actionDesc, err)
			continue
		}

		numSigs := int(vaaBytes[5])
		payload := vaaBytes[6+numSigs*66+51:]
		newIdx := binary.BigEndian.Uint32(payload[35:39])
		fmt.Fprintf(w, "  Entry %2d: signing_index=%d, action_type=%s, target_index=%d ✅\n",
			i, entry.GuardianSetIndex, actionDesc, newIdx)
		fmt.Fprintf(w, "  VAA prefix: %s\n", vaaPrefix(entry.VAA))
		fmt.Fprintf(w, "  Result: PASS ✅\n")
		return entry.VAA
	}

	fmt.Fprintf(w, "  No valid upgrade VAA found for signing index %d → target index %d\n",
		signingIndex, targetIndex)
	fmt.Fprintf(w, "  Result: FAIL ❌\n")
	return ""
}

// extractActionDesc returns the governance action type byte as a string for display,
// or "?" if the VAA is too short to read the payload.
func extractActionDesc(vaaBytes []byte) string {
	if len(vaaBytes) < 6 {
		return "?"
	}
	numSigs := int(vaaBytes[5])
	payloadStart := 6 + numSigs*66 + 51
	if len(vaaBytes) < payloadStart+33 {
		return "?"
	}
	return fmt.Sprintf("%d", vaaBytes[payloadStart+32])
}

func vaaPrefix(b64 string) string {
	if len(b64) > 20 {
		return b64[:20] + "..."
	}
	return b64
}
