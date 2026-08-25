package faults

import (
	"github.com/mrprewsh/mcp-deployment-controller/internal/planning"
	"github.com/mrprewsh/mcp-deployment-controller/internal/policy"
	"testing"
)

func TestLostResponseRequiresExplicitFaultSwitch(t *testing.T) {
	if err := Validate(policy.Config{}, planning.Plan{}, LostResponse); err == nil {
		t.Fatal("fault was accepted while disabled")
	}
}
