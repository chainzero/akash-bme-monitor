package guardian

import (
	"strings"
	"testing"

	"github.com/chainzero/akash-bme-monitor/internal/config"
)

func TestBuildSubmitCommand_ContainsVAA(t *testing.T) {
	network := config.NetworkConfig{WormholeContract: "akash1contract"}
	cmd := buildSubmitCommand(network, "BASE64VAA==")
	if !strings.Contains(cmd, "export VAA='BASE64VAA=='") {
		t.Errorf("command missing VAA export:\n%s", cmd)
	}
}

func TestBuildSubmitCommand_ContainsContract(t *testing.T) {
	network := config.NetworkConfig{WormholeContract: "akash1wormholecontract"}
	cmd := buildSubmitCommand(network, "VAA")
	if !strings.Contains(cmd, "akash1wormholecontract") {
		t.Errorf("command missing contract address:\n%s", cmd)
	}
}

func TestBuildSubmitCommand_WithOperator(t *testing.T) {
	network := config.NetworkConfig{
		WormholeContract: "akash1contract",
		OperatorAddress:  "akash1operator",
	}
	cmd := buildSubmitCommand(network, "VAA")
	if !strings.Contains(cmd, "--from akash1operator") {
		t.Errorf("command missing --from:\n%s", cmd)
	}
}

func TestBuildSubmitCommand_WithChainID(t *testing.T) {
	network := config.NetworkConfig{
		WormholeContract: "akash1contract",
		ChainID:          "akashnet-2",
	}
	cmd := buildSubmitCommand(network, "VAA")
	if !strings.Contains(cmd, "--chain-id akashnet-2") {
		t.Errorf("command missing --chain-id:\n%s", cmd)
	}
}

func TestBuildSubmitCommand_AlwaysHasGasFlags(t *testing.T) {
	network := config.NetworkConfig{WormholeContract: "akash1contract"}
	cmd := buildSubmitCommand(network, "VAA")
	if !strings.Contains(cmd, "--gas auto") {
		t.Errorf("command missing --gas auto:\n%s", cmd)
	}
	if !strings.Contains(cmd, "--gas-adjustment 1.5") {
		t.Errorf("command missing --gas-adjustment 1.5:\n%s", cmd)
	}
}

func TestBuildSubmitCommand_NoOperator_NoFromFlag(t *testing.T) {
	network := config.NetworkConfig{WormholeContract: "akash1contract"}
	cmd := buildSubmitCommand(network, "VAA")
	if strings.Contains(cmd, "--from") {
		t.Errorf("command should not have --from when OperatorAddress is empty:\n%s", cmd)
	}
}

func TestBuildSubmitCommand_NoChainID_NoChainIDFlag(t *testing.T) {
	network := config.NetworkConfig{WormholeContract: "akash1contract"}
	cmd := buildSubmitCommand(network, "VAA")
	if strings.Contains(cmd, "--chain-id") {
		t.Errorf("command should not have --chain-id when ChainID is empty:\n%s", cmd)
	}
}

func TestBuildSubmitCommand_FullConfig(t *testing.T) {
	network := config.NetworkConfig{
		WormholeContract: "akash1contract",
		OperatorAddress:  "akash1op",
		ChainID:          "akashnet-2",
	}
	cmd := buildSubmitCommand(network, "TESTVAA==")

	checks := []string{
		"export VAA='TESTVAA=='",
		"akash1contract",
		"--from akash1op",
		"--chain-id akashnet-2",
		"--gas auto",
		"--gas-adjustment 1.5",
		"-y",
	}
	for _, check := range checks {
		if !strings.Contains(cmd, check) {
			t.Errorf("command missing %q:\n%s", check, cmd)
		}
	}
}
