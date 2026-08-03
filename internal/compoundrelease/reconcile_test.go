package compoundrelease

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/state"
)

type fakeReconcileAdapter struct {
	root        string
	rootErr     error
	outcome     ReleaseChildInvokeOutcome
	resolved    int
	scans       int
	invocations []ReleaseChildInvocation
}

func (f *fakeReconcileAdapter) ResolveSessionRoot(Scope) (string, error) {
	f.resolved++
	if f.rootErr != nil {
		return "", f.rootErr
	}
	return f.root, nil
}

func (f *fakeReconcileAdapter) ScanSessionMessages(root string, now func() time.Time) ([]state.Message, []state.Warning) {
	f.scans++
	return state.ScanSessionMessages(root, now)
}

func (f *fakeReconcileAdapter) InvokeReleaseChild(invocation ReleaseChildInvocation) ReleaseChildInvokeOutcome {
	f.invocations = append(f.invocations, invocation)
	return f.outcome
}

func reconcileFixture(t *testing.T) (*Store, Snapshot, *fakeReconcileAdapter) {
	t.Helper()
	scope := testScope(t)
	store := openTestStore(t, scope)
	planned, err := store.Create(specForScope(scope))
	if err != nil {
		t.Fatal(err)
	}
	publishing, err := store.StartPublishing(planned.Pointer.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(scope.ProjectDir, ".agent-mail", scope.Session)
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	return store, publishing, &fakeReconcileAdapter{root: root}
}

type releaseMessageHeader struct {
	Schema   int            `json:"schema"`
	ID       string         `json:"id"`
	From     string         `json:"from"`
	To       []string       `json:"to"`
	Thread   string         `json:"thread"`
	Subject  string         `json:"subject"`
	Created  string         `json:"created"`
	Priority string         `json:"priority"`
	Kind     string         `json:"kind"`
	Context  map[string]any `json:"context"`
}

func writeReleaseQuestion(t *testing.T, root, owner, mailbox, id, created string, child operatorauth.ReleaseChildPlan, spec operatorauth.ReleaseSpec, mutate func(*releaseMessageHeader)) {
	t.Helper()
	header := releaseMessageHeader{
		Schema: 1, ID: id, From: spec.RequesterHandle, To: []string{spec.OperatorHandle},
		Thread: child.Thread, Subject: child.Subject, Created: created, Priority: "normal", Kind: "question",
		Context: map[string]any{"authorization_request": child.AuthorizationRequest, "release_child": child.ReleaseChild},
	}
	if mutate != nil {
		mutate(&header)
	}
	raw, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "agents", owner, "inbox", mailbox)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := append([]byte("---json\n"), raw...)
	data = append(data, []byte("\n---\n"+child.Body+"\n")...)
	if err := os.WriteFile(filepath.Join(dir, id+".md"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func installExactReleaseQuestion(t *testing.T, publishing Snapshot, adapter *fakeReconcileAdapter, ordinal int, id string) {
	t.Helper()
	child := publishing.Prepared.Children[ordinal]
	writeReleaseQuestion(t, adapter.root, publishing.Prepared.Spec.OperatorHandle, "new", id, "2026-07-15T01:00:00Z", child, publishing.Prepared.Spec, nil)
}

func stageAllReconcileEvidence(t *testing.T, store *Store, publishing Snapshot, adapter *fakeReconcileAdapter) {
	t.Helper()
	if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
		t.Fatal(err)
	}
	installExactReleaseQuestion(t, publishing, adapter, 0, "question-tag")
	if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, 0, "question-tag"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 1); err != nil {
		t.Fatal(err)
	}
	installExactReleaseQuestion(t, publishing, adapter, 1, "question-github-release")
	if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, 1, "question-github-release"); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileAcceptedSendPersistsTransportMessageIdentity(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	adapter.outcome = ReleaseChildInvokeOutcome{ProcessStarted: true, InvocationBegan: true, MessageID: "question-tag"}

	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err != nil || result.Disposition != ReconcilePublished || result.Role != operatorauth.ReleaseChildTag {
		t.Fatalf("Reconcile()=(%+v,%v)", result, err)
	}
	if len(adapter.invocations) != 1 {
		t.Fatalf("invocations=%d", len(adapter.invocations))
	}
	invocation := adapter.invocations[0]
	child := publishing.Prepared.Children[0]
	if invocation.AttemptID != child.ReleaseChild.AttemptID || invocation.Sender != publishing.Prepared.Spec.RequesterHandle || invocation.Recipient != publishing.Prepared.Spec.OperatorHandle || invocation.Thread != child.Thread || invocation.AuthorizationRequest != child.AuthorizationRequest || invocation.ReleaseChild != child.ReleaseChild {
		t.Fatalf("invocation=%+v child=%+v", invocation, child)
	}
	record := mustGenerationRecord(t, store, 1)
	if record.Children[0].State != childPublicationPublished || record.Children[0].QuestionMessageID != "question-tag" || record.Children[1].State != childPublicationPlanned {
		t.Fatalf("record=%+v", record)
	}
}

func TestReconcilePreInvocationFailureRollsBackClaim(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	adapter.outcome = ReleaseChildInvokeOutcome{Err: errors.New("exec rejected")}

	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err == nil || result.Disposition != ReconcileAmbiguous || result.Role != operatorauth.ReleaseChildTag {
		t.Fatalf("Reconcile()=(%+v,%v)", result, err)
	}
	record := mustGenerationRecord(t, store, 1)
	if record.Children[0].State != childPublicationPlanned || record.Children[0].ClaimRevision != 1 || record.Children[0].ClaimToken != "" || record.Children[0].QuestionMessageID != "" {
		t.Fatalf("rolled back child=%+v", record.Children[0])
	}
}

func TestReconcileSuccessWithoutMessageIdentityIsAmbiguous(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	adapter.outcome = ReleaseChildInvokeOutcome{ProcessStarted: true, InvocationBegan: true}

	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err == nil || result.Disposition != ReconcileAmbiguous || result.Role != operatorauth.ReleaseChildTag {
		t.Fatalf("Reconcile()=(%+v,%v)", result, err)
	}
	record := mustGenerationRecord(t, store, 1)
	if record.Children[0].State != childPublicationSending || record.Children[0].QuestionMessageID != "" {
		t.Fatalf("uncertain child=%+v", record.Children[0])
	}
}

func TestReconcileCrashRecoveryAdoptsExactMailboxMessageWithoutResend(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
		t.Fatal(err)
	}
	installExactReleaseQuestion(t, publishing, adapter, 0, "question-tag")
	adapter.outcome = ReleaseChildInvokeOutcome{ProcessStarted: true, InvocationBegan: true, MessageID: "question-github-release"}

	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err != nil || result.Disposition != ReconcilePublished || result.Role != operatorauth.ReleaseChildGitHubRelease {
		t.Fatalf("Reconcile()=(%+v,%v)", result, err)
	}
	if len(adapter.invocations) != 1 || adapter.invocations[0].Role != operatorauth.ReleaseChildGitHubRelease {
		t.Fatalf("invocations=%+v", adapter.invocations)
	}
	record := mustGenerationRecord(t, store, 1)
	if record.Children[0].State != childPublicationPublished || record.Children[0].QuestionMessageID != "question-tag" || record.Children[1].State != childPublicationPublished || record.Children[1].QuestionMessageID != "question-github-release" {
		t.Fatalf("record=%+v", record)
	}
}

func TestReconcileDuplicateExactMessagesTerminalizeConflict(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
		t.Fatal(err)
	}
	installExactReleaseQuestion(t, publishing, adapter, 0, "question-tag-a")
	installExactReleaseQuestion(t, publishing, adapter, 0, "question-tag-b")

	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err != nil || result.Disposition != ReconcileConflict || result.Role != operatorauth.ReleaseChildTag {
		t.Fatalf("Reconcile()=(%+v,%v)", result, err)
	}
	if len(adapter.invocations) != 0 {
		t.Fatalf("conflict invoked transport: %+v", adapter.invocations)
	}
	record := mustGenerationRecord(t, store, 1)
	if record.State != operatorauth.ReleaseStateConflict || !reflect.DeepEqual(record.Children[0].ObservedMessageIDs, []string{"question-tag-a", "question-tag-b"}) {
		t.Fatalf("record=%+v", record)
	}
}

func TestReconcilePublishedMessageIdentityDivergenceConflicts(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, 0, "question-tag-original"); err != nil {
		t.Fatal(err)
	}
	installExactReleaseQuestion(t, publishing, adapter, 0, "question-tag-other")

	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err != nil || result.Disposition != ReconcileConflict || result.Role != operatorauth.ReleaseChildTag {
		t.Fatalf("Reconcile()=(%+v,%v)", result, err)
	}
	if len(adapter.invocations) != 0 {
		t.Fatalf("identity conflict invoked transport: %+v", adapter.invocations)
	}
}

func TestReconcilePublishedMissingStoredMessageIsRecordInvalid(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	if _, err := store.ClaimChildSend(publishing.Pointer.GenerationID, 0); err != nil {
		t.Fatal(err)
	}
	const missingID = "question-tag-missing"
	if err := store.AdoptChildPublication(publishing.Pointer.GenerationID, 0, missingID); err != nil {
		t.Fatal(err)
	}

	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err != nil || result.Disposition != ReconcileConflict || result.Role != operatorauth.ReleaseChildTag {
		t.Fatalf("Reconcile()=(%+v,%v)", result, err)
	}
	record := mustGenerationRecord(t, store, 1)
	recordPath := filepath.Join(store.dirPath, store.generationName(1))
	child := record.Children[0]
	if record.State != operatorauth.ReleaseStateConflict || child.QuestionMessageID != missingID || !strings.Contains(child.ConflictReason, "record_invalid") || !strings.Contains(child.ConflictReason, recordPath) || !strings.Contains(child.ConflictReason, missingID) {
		t.Fatalf("record=%+v path=%q", record, recordPath)
	}
}

func TestReconcileActivatesAfterBothExactMailboxMessages(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	stageAllReconcileEvidence(t, store, publishing, adapter)

	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err != nil || result.Disposition != ReconcileActivated || result.Snapshot.Pointer.State != operatorauth.ReleaseStateActive || result.Snapshot.Active == nil {
		t.Fatalf("Reconcile()=(%+v,%v)", result, err)
	}
	if len(adapter.invocations) != 0 {
		t.Fatalf("activation invoked transport: %+v", adapter.invocations)
	}
	if result.Snapshot.Active.Children[0].QuestionMessageID != "question-tag" || result.Snapshot.Active.Children[1].QuestionMessageID != "question-github-release" {
		t.Fatalf("active=%+v", result.Snapshot.Active)
	}
	resolved, scans := adapter.resolved, adapter.scans
	again, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err != nil || again.Disposition != ReconcileActivated || adapter.resolved != resolved || adapter.scans != scans {
		t.Fatalf("terminal replay=%+v err=%v adapter=%+v", again, err, adapter)
	}
}

func TestReconcileNearMessageConflictsBeforeInvocation(t *testing.T) {
	store, publishing, adapter := reconcileFixture(t)
	child := publishing.Prepared.Children[0]
	writeReleaseQuestion(t, adapter.root, publishing.Prepared.Spec.OperatorHandle, "new", "near-tag", "2026-07-15T01:00:00Z", child, publishing.Prepared.Spec, func(header *releaseMessageHeader) {
		header.Subject += " changed"
	})

	result, err := store.Reconcile(publishing.Pointer.GenerationID, adapter)
	if err != nil || result.Disposition != ReconcileConflict || result.Role != operatorauth.ReleaseChildTag {
		t.Fatalf("Reconcile()=(%+v,%v)", result, err)
	}
	if len(adapter.invocations) != 0 {
		t.Fatalf("near message invoked transport: %+v", adapter.invocations)
	}
}
