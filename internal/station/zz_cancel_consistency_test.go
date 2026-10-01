package station

import (
	"context"
	"errors"
	"sync"
	"testing"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

// cancelAfterContext reports cancellation once Err has been consulted more
// than `remaining` times, so a test can deterministically cancel a request
// between the service entry check and the repository commit boundary.
type cancelAfterContext struct {
	context.Context
	mu        sync.Mutex
	remaining int
}

func (c *cancelAfterContext) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.remaining <= 0 {
		return context.Canceled
	}
	c.remaining--
	return c.Context.Err()
}

// cancelsAtCommitBoundary allows the service entry check to pass and then
// reports cancellation at the repository commit boundary.
func cancelsAtCommitBoundary() context.Context {
	return &cancelAfterContext{Context: context.Background(), remaining: 1}
}

func specimenRequest(catalog string) contracts.RegisterSpecimenRequest {
	return contracts.RegisterSpecimenRequest{
		CatalogNumber: catalog,
		FieldLabel:    "北坡-07",
		TaxonHint:     "大型脊椎动物",
		MatrixKind:    "粉砂岩",
		JacketNotes:   "夹克边角完好",
	}
}

func planRequest() contracts.CreatePlanRequest {
	return contracts.CreatePlanRequest{
		Objective:      "暴露主要关节面",
		Method:         "手工显微剥离",
		Adhesive:       "可逆树脂",
		TargetExposure: 80,
	}
}

func mustRegister(t *testing.T, s *Service, catalog string) domain.Specimen {
	t.Helper()
	value, err := s.RegisterSpecimen(context.Background(), specimenRequest(catalog))
	if err != nil {
		t.Fatalf("register specimen: %v", err)
	}
	return value
}

func mustCreatePlan(t *testing.T, s *Service, specimenID string) domain.PrepPlan {
	t.Helper()
	value, err := s.CreatePlan(context.Background(), specimenID, planRequest())
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	return value
}

func mustApprove(t *testing.T, s *Service, planID string) domain.PrepPlan {
	t.Helper()
	value, err := s.ApprovePlan(context.Background(), planID, contracts.ApprovalRequest{ApprovedBy: "curator-lin"})
	if err != nil {
		t.Fatalf("approve plan: %v", err)
	}
	return value
}

func mustLogProgress(t *testing.T, s *Service, planID string, percent int) {
	t.Helper()
	_, err := s.LogProgress(context.Background(), planID, contracts.ProgressRequest{
		Stage:      "表面加固",
		Percent:    percent,
		Note:       "测试记录",
		RecordedBy: "preparator-chen",
	})
	if err != nil {
		t.Fatalf("log progress %d: %v", percent, err)
	}
}

func progressRequest(percent int) contracts.ProgressRequest {
	return contracts.ProgressRequest{
		Stage:      "表面加固",
		Percent:    percent,
		Note:       "测试记录",
		RecordedBy: "preparator-chen",
	}
}

func releaseRequest() contracts.ReleaseRequest {
	return contracts.ReleaseRequest{
		Purpose:    "比较解剖研究",
		Recipient:  "地层研究组",
		ApprovedBy: "keeper-zhou",
	}
}

func TestRegisterSpecimenCancelledAtCommitBoundary(t *testing.T) {
	service := New(repository.NewMemory())
	if _, err := service.RegisterSpecimen(cancelsAtCommitBoundary(), specimenRequest("PPS-9001")); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	items, err := service.ListSpecimens(context.Background())
	if err != nil {
		t.Fatalf("list specimens: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("cancelled registration left %d specimens", len(items))
	}
	if _, err := service.RegisterSpecimen(context.Background(), specimenRequest("PPS-9001")); err != nil {
		t.Fatalf("retry after cancellation must not conflict, got %v", err)
	}
}

func TestCreatePlanCancelledAtCommitBoundary(t *testing.T) {
	service := New(repository.NewMemory())
	specimen := mustRegister(t, service, "PPS-9002")
	if _, err := service.CreatePlan(cancelsAtCommitBoundary(), specimen.ID, planRequest()); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	plans, err := service.ListPlansForSpecimen(context.Background(), specimen.ID)
	if err != nil {
		t.Fatalf("list plans: %v", err)
	}
	if len(plans) != 0 {
		t.Fatalf("cancelled plan creation left %d plans", len(plans))
	}
	reloaded, err := service.GetSpecimen(context.Background(), specimen.ID)
	if err != nil {
		t.Fatalf("get specimen: %v", err)
	}
	if reloaded.Status != domain.SpecimenRegistered || reloaded.Version != specimen.Version {
		t.Fatalf("specimen mutated by cancelled plan creation: %+v", reloaded)
	}
	if _, err := service.CreatePlan(context.Background(), specimen.ID, planRequest()); err != nil {
		t.Fatalf("retry after cancellation must not conflict, got %v", err)
	}
}

func TestApprovePlanCancelledAtCommitBoundary(t *testing.T) {
	service := New(repository.NewMemory())
	specimen := mustRegister(t, service, "PPS-9003")
	plan := mustCreatePlan(t, service, specimen.ID)
	if _, err := service.ApprovePlan(cancelsAtCommitBoundary(), plan.ID, contracts.ApprovalRequest{ApprovedBy: "curator-lin"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	snapshot, err := service.GetPlan(context.Background(), plan.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if snapshot.Plan.Status != domain.PlanDraft || snapshot.Plan.Version != plan.Version {
		t.Fatalf("plan mutated by cancelled approval: %+v", snapshot.Plan)
	}
	reloaded, err := service.GetSpecimen(context.Background(), specimen.ID)
	if err != nil {
		t.Fatalf("get specimen: %v", err)
	}
	if reloaded.Status != domain.SpecimenPlanned {
		t.Fatalf("specimen mutated by cancelled approval: %+v", reloaded)
	}
	approved, err := service.ApprovePlan(context.Background(), plan.ID, contracts.ApprovalRequest{ApprovedBy: "curator-lin"})
	if err != nil {
		t.Fatalf("retry after cancellation must not conflict, got %v", err)
	}
	if approved.Status != domain.PlanApproved || approved.Version != plan.Version+1 {
		t.Fatalf("retry did not apply the normal transition: %+v", approved)
	}
}

func TestLogProgressCancelledAtCommitBoundary(t *testing.T) {
	service := New(repository.NewMemory())
	specimen := mustRegister(t, service, "PPS-9004")
	plan := mustCreatePlan(t, service, specimen.ID)
	mustApprove(t, service, plan.ID)
	if _, err := service.LogProgress(cancelsAtCommitBoundary(), plan.ID, progressRequest(100)); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	snapshot, err := service.GetPlan(context.Background(), plan.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if len(snapshot.Progress) != 0 {
		t.Fatalf("cancelled progress left %d entries", len(snapshot.Progress))
	}
	if snapshot.Plan.Status != domain.PlanApproved {
		t.Fatalf("plan mutated by cancelled progress: %+v", snapshot.Plan)
	}
	reloaded, err := service.GetSpecimen(context.Background(), specimen.ID)
	if err != nil {
		t.Fatalf("get specimen: %v", err)
	}
	if reloaded.Status != domain.SpecimenPreparing {
		t.Fatalf("specimen mutated by cancelled progress: %+v", reloaded)
	}
	// Retry reaches the terminal state; both read entry points must agree.
	mustLogProgress(t, service, plan.ID, 100)
	snapshot, err = service.GetPlan(context.Background(), plan.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	reloaded, err = service.GetSpecimen(context.Background(), specimen.ID)
	if err != nil {
		t.Fatalf("get specimen: %v", err)
	}
	if snapshot.Plan.Status != domain.PlanCompleted || reloaded.Status != domain.SpecimenStabilized {
		t.Fatalf("terminal state inconsistent: plan=%s specimen=%s", snapshot.Plan.Status, reloaded.Status)
	}
}

func TestReleaseSpecimenCancelledAtCommitBoundary(t *testing.T) {
	service := New(repository.NewMemory())
	specimen := mustRegister(t, service, "PPS-9005")
	plan := mustCreatePlan(t, service, specimen.ID)
	mustApprove(t, service, plan.ID)
	mustLogProgress(t, service, plan.ID, 100)
	if _, err := service.ReleaseSpecimen(cancelsAtCommitBoundary(), specimen.ID, releaseRequest()); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	reloaded, err := service.GetSpecimen(context.Background(), specimen.ID)
	if err != nil {
		t.Fatalf("get specimen: %v", err)
	}
	if reloaded.Status != domain.SpecimenStabilized {
		t.Fatalf("specimen mutated by cancelled release: %+v", reloaded)
	}
	if _, err := service.ReleaseSpecimen(context.Background(), specimen.ID, releaseRequest()); err != nil {
		t.Fatalf("retry after cancellation must not conflict, got %v", err)
	}
	reloaded, err = service.GetSpecimen(context.Background(), specimen.ID)
	if err != nil {
		t.Fatalf("get specimen: %v", err)
	}
	if reloaded.Status != domain.SpecimenAvailable {
		t.Fatalf("retry did not release the specimen: %+v", reloaded)
	}
}

func TestPreCancelledRequestFailsBeforeMutation(t *testing.T) {
	service := New(repository.NewMemory())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.RegisterSpecimen(ctx, specimenRequest("PPS-9006")); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	items, err := service.ListSpecimens(context.Background())
	if err != nil {
		t.Fatalf("list specimens: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("pre-cancelled registration left %d specimens", len(items))
	}
}
