package cli

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// #589: `amq-squad amq send` wrote state=ambiguous_unknown with an empty
// message_id for a send AMQ had refused fail-closed. A send that definitively
// never left must produce a definite failed receipt; ambiguous_unknown is
// reserved for genuinely unknown outcomes, and using it for a provable
// non-delivery makes the receipt trail misleading during outage forensics —
// exactly when it is being read most carefully.
//
// These tests are deliberately BOTH WAYS. A change that classified everything
// as failed would satisfy the first half and would be far more dangerous than
// the defect it replaced.

// exitCodeErr builds an *exec.ExitError carrying a real exit status, so the
// classifier is exercised through the same type production sees rather than
// through a stub that merely reports a number.
func exitCodeErr(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("building exit-%d error: got %v", code, err)
	}
	if exitErr.ExitCode() != code {
		t.Fatalf("exit code = %d, want %d", exitErr.ExitCode(), code)
	}
	return err
}

func TestFailClosedSendProducesDefiniteFailedReceipt(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
	}{
		// Observed against the pinned AMQ 0.51.1:
		//   5 -> "refusing send: root is not the pinned base root..."
		//   2 -> "--to is required", unknown command, bad flag
		{"explicit refusal", amqExitRefused},
		{"usage rejection", amqExitUsageRejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receipt := &deliveryReceiptData{
				AttemptID: "attempt-589",
				Consumers: []deliveryConsumerState{{Consumer: "cto", State: deliveryStateAmbiguousUnknown}},
			}
			markDeliverySendResult(receipt, []byte("refusing send: root is not the pinned base root\n"), exitCodeErr(t, tc.code))

			if receipt.DeliveryState != deliveryStateFailed {
				t.Fatalf("DeliveryState = %q, want %q — a refused send has a definite outcome", receipt.DeliveryState, deliveryStateFailed)
			}
			if receipt.FailedAt == nil {
				t.Error("FailedAt is nil; a definite failure must carry its timestamp")
			}
			if receipt.MessageID != "" {
				t.Errorf("MessageID = %q, want empty", receipt.MessageID)
			}
			for _, consumer := range receipt.Consumers {
				if consumer.State != deliveryStateFailed {
					t.Errorf("consumer %s state = %q, want %q", consumer.Consumer, consumer.State, deliveryStateFailed)
				}
			}
		})
	}
}

// TestGenuinelyAmbiguousSendStaysAmbiguous is the half that protects against
// over-claiming. An outcome that is actually unknown must remain unknown: a
// receipt asserting definite failure invites a retry that could duplicate a
// message which did land.
func TestGenuinelyAmbiguousSendStaysAmbiguous(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		err  error
	}{
		{"timeout", "", errors.New("signal: killed")},
		{"unclassified nonzero exit", "", exitCodeErr(t, 1)},
		{"crash mid-commit", "partial output", exitCodeErr(t, 137)},
		{"plain wrapped failure", "", errors.New("amq send: connection reset")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receipt := &deliveryReceiptData{
				AttemptID: "attempt-589",
				Consumers: []deliveryConsumerState{{Consumer: "cto", State: deliveryStateAmbiguousUnknown}},
			}
			markDeliverySendResult(receipt, []byte(tc.out), tc.err)
			if receipt.DeliveryState != deliveryStateAmbiguousUnknown {
				t.Fatalf("DeliveryState = %q, want %q — this outcome is genuinely unknown and must not be reported as definite", receipt.DeliveryState, deliveryStateAmbiguousUnknown)
			}
			if receipt.FailedAt != nil {
				t.Error("FailedAt was set for an ambiguous outcome")
			}
		})
	}
}

// TestFailClosedClassifierRequiresPositiveEvidence pins the predicate directly,
// including the wrapped-error fallback. The fallback requires AMQ's stable
// refusal marker rather than loose failure words, so an ambiguous outcome that
// merely mentions failing is never misread as definite.
func TestFailClosedClassifierRequiresPositiveEvidence(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		err  error
		want bool
	}{
		{"nil error", "", nil, false},
		{"refusal exit code", "", exitCodeErr(t, amqExitRefused), true},
		{"usage exit code", "", exitCodeErr(t, amqExitUsageRejected), true},
		{"refusal marker in wrapped error", "", fmt.Errorf("amq send: %s root is not pinned", amqRefusalMarker), true},
		{"refusal marker in output", amqRefusalMarker + " bad root", errors.New("exit status 5"), true},
		{"generic failure wording", "", errors.New("send failed: could not reach the queue"), false},
		{"the word refusing without the marker", "", errors.New("server is refusing connections"), false},
		{"unrelated nonzero exit", "", exitCodeErr(t, 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := amqRefusedSendFailClosed([]byte(tc.out), tc.err); got != tc.want {
				t.Errorf("amqRefusedSendFailClosed(%q, %v) = %t, want %t", tc.out, tc.err, got, tc.want)
			}
		})
	}
}

// TestFailClosedReceiptStageExplainsTheDifference: the stage text is what an
// operator reads during an outage. It must say non-delivery is definite and
// point at the refusal cause, not tell them to go confirm delivery.
func TestFailClosedReceiptStageExplainsTheDifference(t *testing.T) {
	receipt := &deliveryReceiptData{AttemptID: "attempt-589"}
	markDeliverySendResult(receipt, nil, exitCodeErr(t, amqExitRefused))
	if len(receipt.Stages) == 0 {
		t.Fatal("no stage recorded for a fail-closed send")
	}
	last := receipt.Stages[len(receipt.Stages)-1]
	if !strings.Contains(last.Detail, "definite") {
		t.Errorf("stage detail does not tell the operator the outcome is definite: %q", last.Detail)
	}
	if strings.Contains(last.Detail, "confirm non-delivery before retry") {
		t.Errorf("fail-closed stage still carries the ambiguous-outcome instruction: %q", last.Detail)
	}
}

// TestSuccessfulSendUnaffected guards the path that must not move at all.
func TestSuccessfulSendUnaffected(t *testing.T) {
	receipt := &deliveryReceiptData{
		AttemptID: "attempt-589",
		Consumers: []deliveryConsumerState{{Consumer: "cto"}},
	}
	markDeliverySendResult(receipt, []byte("Sent 2026-08-02T00-00-00.000Z_pid1_abcdef to cto\n"), nil)
	if receipt.DeliveryState != deliveryStateDeliveredNotDrained {
		t.Fatalf("DeliveryState = %q, want %q", receipt.DeliveryState, deliveryStateDeliveredNotDrained)
	}
	if receipt.MessageID == "" {
		t.Error("MessageID was not parsed from a successful send")
	}
}
