// Package approval binds a human decision to an immutable deployment plan.
package approval

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/mrprewsh/mcp-deployment-controller/internal/planning"
	"github.com/mrprewsh/mcp-deployment-controller/internal/policy"
)

type Decision string

const (
	DecisionApproved Decision = "APPROVED"
	DecisionDenied   Decision = "DENIED"
)

type Approval struct {
	ID        string    `json:"id"`
	PlanID    string    `json:"plan_id"`
	PlanHash  string    `json:"plan_hash"`
	Decision  Decision  `json:"decision"`
	Actor     string    `json:"actor"`
	CreatedAt time.Time `json:"created_at"`
}

type Store interface {
	GetPlan(context.Context, string) (planning.Plan, error)
	SaveApproval(context.Context, Approval) error
	GetApproval(context.Context, string) (Approval, error)
}

type Service struct {
	store  Store
	config policy.Config
	now    func() time.Time
}

func NewService(store Store, config policy.Config) *Service {
	return &Service{store: store, config: config, now: func() time.Time { return time.Now().UTC() }}
}

// Decide writes one immutable decision. A denial never enables execution; an
// approval is accepted only if it names the exact stored plan hash and policy
// admits that plan at approval time.
func (s *Service) Decide(ctx context.Context, planID, planHash string, decision Decision, actor string) (Approval, error) {
	if s.store == nil || actor == "" {
		return Approval{}, errors.New("approval store and actor are required")
	}
	if decision != DecisionApproved && decision != DecisionDenied {
		return Approval{}, errors.New("decision must be APPROVED or DENIED")
	}
	plan, err := s.store.GetPlan(ctx, planID)
	if err != nil {
		return Approval{}, err
	}
	if err := planning.VerifyHash(plan); err != nil {
		return Approval{}, err
	}
	if plan.Hash != planHash {
		return Approval{}, errors.New("approval hash does not match the stored plan")
	}
	if decision == DecisionApproved {
		if err := policy.Validate(s.config, plan); err != nil {
			return Approval{}, err
		}
	}
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return Approval{}, fmt.Errorf("generate approval id: %w", err)
	}
	approval := Approval{ID: "apr_" + hex.EncodeToString(bytes), PlanID: planID, PlanHash: planHash, Decision: decision, Actor: actor, CreatedAt: s.now()}
	if err := s.store.SaveApproval(ctx, approval); err != nil {
		return Approval{}, err
	}
	return approval, nil
}
