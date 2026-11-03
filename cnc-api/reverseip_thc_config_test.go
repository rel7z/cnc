package cnc

import (
	"strings"
	"testing"
)

func TestReverseIPTHCLoadBalancedConfig(t *testing.T) {
	tool, exists := ToolsRegistry["reverseip-thc"]
	if !exists {
		t.Fatal("reverseip-thc not found in ToolsRegistry")
	}

	// Verify it's now a worker-scope tool for load balancing
	if tool.Scope != "worker" {
		t.Errorf("Expected reverseip-thc Scope to be 'worker' (for load balancing), got '%s'", tool.Scope)
	}

	// Verify it has spread mode for distribution
	if tool.DefaultMode != "spread" {
		t.Errorf("Expected reverseip-thc DefaultMode to be 'spread', got '%s'", tool.DefaultMode)
	}

	// Verify it still has the correct flags
	if tool.InputFlag != "-f" {
		t.Errorf("Expected InputFlag '-f', got '%s'", tool.InputFlag)
	}
	if tool.OutputFlag != "-o" {
		t.Errorf("Expected OutputFlag '-o', got '%s'", tool.OutputFlag)
	}

	// Verify description mentions load balancing
	if !strings.Contains(strings.ToLower(tool.Description), "load") {
		t.Errorf("Expected description to mention load balancing, got: %s", tool.Description)
	}

	t.Logf("✓ reverseip-thc is properly configured for load balancing")
	t.Logf("  Scope: %s", tool.Scope)
	t.Logf("  DefaultMode: %s", tool.DefaultMode)
	t.Logf("  Description: %s", tool.Description)
}
