package guardian

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

// --- decodeVAAFromCalldata ---

// makeCalldata constructs ABI-encoded submitNewGuardianSet calldata containing vaaBytes.
func makeCalldata(vaaBytes []byte) string {
	// ABI encoding of submitNewGuardianSet(bytes _vm):
	//   4-byte selector
	//   word1: offset (0x20 = 32)
	//   word2: length of bytes data
	//   data:  raw bytes
	selector := submitNewGuardianSetSelector

	word1 := strings.Repeat("0", 62) + "20"
	word2 := fmt.Sprintf("%064x", len(vaaBytes))
	data := hex.EncodeToString(vaaBytes)

	return selector + word1 + word2 + data
}

func TestDecodeVAAFromCalldata_Valid(t *testing.T) {
	original := []byte("this is a test vaa payload")
	calldata := makeCalldata(original)

	got, err := decodeVAAFromCalldata(calldata)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != string(original) {
		t.Errorf("decoded = %x, want %x", got, original)
	}
}

func TestDecodeVAAFromCalldata_TooShort(t *testing.T) {
	// Less than 128 param hex chars after stripping selector.
	_, err := decodeVAAFromCalldata(submitNewGuardianSetSelector + strings.Repeat("0", 100))
	if err == nil {
		t.Fatal("expected error for short calldata")
	}
}

func TestDecodeVAAFromCalldata_TruncatedData(t *testing.T) {
	// Length claims 100 bytes but data is only 10 bytes.
	selector := submitNewGuardianSetSelector
	word1 := strings.Repeat("0", 62) + "20"
	word2 := fmt.Sprintf("%064x", 100) // claims 100 bytes
	data := hex.EncodeToString([]byte("tooshort"))

	_, err := decodeVAAFromCalldata(selector + word1 + word2 + data)
	if err == nil {
		t.Fatal("expected error for truncated data")
	}
}

func TestDecodeVAAFromCalldata_ZeroLengthVAA(t *testing.T) {
	calldata := makeCalldata([]byte{})
	got, err := decodeVAAFromCalldata(calldata)
	if err != nil {
		t.Fatalf("unexpected error for zero-length VAA: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty result, got %x", got)
	}
}

func TestDecodeVAAFromCalldata_LargePayload(t *testing.T) {
	large := make([]byte, 512)
	for i := range large {
		large[i] = byte(i % 256)
	}
	calldata := makeCalldata(large)

	got, err := decodeVAAFromCalldata(calldata)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(large) {
		t.Errorf("got %d bytes, want %d", len(got), len(large))
	}
}

// --- validateGuardianSetUpgradeVAA ---

// makeValidVAA constructs a minimal valid guardian set upgrade VAA for newIndex.
func makeValidVAA(newIndex uint32) []byte {
	var vaa []byte

	// version (1 byte)
	vaa = append(vaa, 0x01)

	// signingIndex (4 bytes) = newIndex - 1
	sigBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(sigBuf, newIndex-1)
	vaa = append(vaa, sigBuf...)

	// numSigs (1 byte) = 0 (no signatures needed for validation test)
	vaa = append(vaa, 0x00)

	// body: 51 zero bytes (timestamp 4 + nonce 4 + emitter_chain 2 + emitter_addr 32 + sequence 8 + consistency 1)
	vaa = append(vaa, make([]byte, 51)...)

	// payload:
	// module: 28 zero bytes + "Core"
	module := make([]byte, 32)
	copy(module[28:], []byte("Core"))
	vaa = append(vaa, module...)

	// action: 0x02 (guardian set upgrade)
	vaa = append(vaa, 0x02)

	// chain: 2 bytes (0 = all chains)
	vaa = append(vaa, 0x00, 0x00)

	// newIndex (4 bytes)
	idxBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(idxBuf, newIndex)
	vaa = append(vaa, idxBuf...)

	return vaa
}

func TestValidateGuardianSetUpgradeVAA_Valid(t *testing.T) {
	vaa := makeValidVAA(5)
	if err := validateGuardianSetUpgradeVAA(vaa, 5); err != nil {
		t.Errorf("unexpected error for valid VAA: %v", err)
	}
}

func TestValidateGuardianSetUpgradeVAA_TooShort(t *testing.T) {
	err := validateGuardianSetUpgradeVAA([]byte{0x01, 0x00, 0x00}, 1)
	if err == nil {
		t.Fatal("expected error for short VAA")
	}
}

func TestValidateGuardianSetUpgradeVAA_WrongSigningIndex(t *testing.T) {
	vaa := makeValidVAA(5) // signing index = 4
	// Tamper: set signing index to 3 (should be 4 for upgrade to 5)
	binary.BigEndian.PutUint32(vaa[1:5], 3)
	if err := validateGuardianSetUpgradeVAA(vaa, 5); err == nil {
		t.Fatal("expected error for wrong signing index")
	}
}

func TestValidateGuardianSetUpgradeVAA_WrongModule(t *testing.T) {
	vaa := makeValidVAA(5)
	// Corrupt the module bytes (overwrite "Core" suffix)
	// payloadStart = 6 + 0*66 + 51 = 57
	payloadStart := 57
	copy(vaa[payloadStart:payloadStart+32], make([]byte, 32)) // zero out module
	if err := validateGuardianSetUpgradeVAA(vaa, 5); err == nil {
		t.Fatal("expected error for wrong module")
	}
}

func TestValidateGuardianSetUpgradeVAA_WrongAction(t *testing.T) {
	vaa := makeValidVAA(5)
	// payloadStart = 57; action is at payloadStart + 32
	payloadStart := 57
	vaa[payloadStart+32] = 0x01 // action 1 = contract upgrade, not guardian set upgrade
	if err := validateGuardianSetUpgradeVAA(vaa, 5); err == nil {
		t.Fatal("expected error for wrong action type")
	}
}

func TestValidateGuardianSetUpgradeVAA_WrongTargetIndex(t *testing.T) {
	vaa := makeValidVAA(5) // targets index 5
	// Validate against index 6 — should fail because the VAA targets 5, not 6.
	if err := validateGuardianSetUpgradeVAA(vaa, 6); err == nil {
		t.Fatal("expected error when VAA index doesn't match target")
	}
}

func TestValidateGuardianSetUpgradeVAA_MultipleSignatures(t *testing.T) {
	// VAA with 2 signatures — payloadStart shifts by 2*66 bytes.
	newIndex := uint32(3)
	var vaa []byte

	vaa = append(vaa, 0x01) // version
	sigBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(sigBuf, newIndex-1)
	vaa = append(vaa, sigBuf...) // signingIndex = 2
	vaa = append(vaa, 0x02)      // numSigs = 2
	vaa = append(vaa, make([]byte, 2*66)...) // 2 dummy signatures
	vaa = append(vaa, make([]byte, 51)...)   // body

	// payload
	module := make([]byte, 32)
	copy(module[28:], []byte("Core"))
	vaa = append(vaa, module...)
	vaa = append(vaa, 0x02)       // action
	vaa = append(vaa, 0x00, 0x00) // chain
	idxBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(idxBuf, newIndex)
	vaa = append(vaa, idxBuf...)

	if err := validateGuardianSetUpgradeVAA(vaa, newIndex); err != nil {
		t.Errorf("unexpected error for VAA with 2 signatures: %v", err)
	}
}

func TestValidateGuardianSetUpgradeVAA_PayloadTooShort(t *testing.T) {
	// Valid header/body but payload is truncated (missing the target index bytes).
	newIndex := uint32(2)
	var vaa []byte
	vaa = append(vaa, 0x01)
	sigBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(sigBuf, newIndex-1)
	vaa = append(vaa, sigBuf...)
	vaa = append(vaa, 0x00)
	vaa = append(vaa, make([]byte, 51)...)
	// payload: only module + action (missing chain + index)
	module := make([]byte, 32)
	copy(module[28:], []byte("Core"))
	vaa = append(vaa, module...)
	vaa = append(vaa, 0x02) // action only — no chain, no index

	if err := validateGuardianSetUpgradeVAA(vaa, newIndex); err == nil {
		t.Fatal("expected error for truncated payload")
	}
}
