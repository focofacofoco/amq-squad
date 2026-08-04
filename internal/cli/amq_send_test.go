package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestRunOwnedAMQSendUsesTransportAcceptanceWithoutLocalReceipt(t *testing.T) {
	previous := runAMQCommand
	t.Cleanup(func() { runAMQCommand = previous })
	calls := 0
	runAMQCommand = func(amqCommandRequest) ([]byte, error) {
		calls++
		return []byte("Sent msg-accepted to qa\n"), nil
	}

	out, result, err := runOwnedAMQSend(ownedAMQSendOptions{}, amqCommandRequest{Arg: []string{"send"}})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || string(out) != "Sent msg-accepted to qa\n" || result.MessageID != "msg-accepted" || !result.Invoked || result.Reconciled {
		t.Fatalf("calls=%d out=%q result=%+v", calls, out, result)
	}
}

func TestRunOwnedAMQSendPreservesStableIDAlongsideTransportError(t *testing.T) {
	previous := runAMQCommand
	t.Cleanup(func() { runAMQCommand = previous })
	sendErr := errors.New("wait for drained timed out")
	runAMQCommand = func(amqCommandRequest) ([]byte, error) {
		return []byte("Sent msg-timeout to qa\n"), sendErr
	}

	_, result, err := runOwnedAMQSend(ownedAMQSendOptions{}, amqCommandRequest{Arg: []string{"send"}})
	if !errors.Is(err, sendErr) || result.MessageID != "msg-timeout" || !result.Invoked || result.Reconciled {
		t.Fatalf("err=%v result=%+v", err, result)
	}
}

func TestRunOwnedAMQSendReconcilesWithoutTransportInvocation(t *testing.T) {
	previous := runAMQCommand
	t.Cleanup(func() { runAMQCommand = previous })
	runAMQCommand = func(amqCommandRequest) ([]byte, error) {
		t.Fatal("reconciled send invoked AMQ")
		return nil, nil
	}
	boundary, err := newDurableInvocationBoundary(func(func() error) (durableInvocationResult, error) {
		return newDurableReconciledExistingResult("msg-existing")
	})
	if err != nil {
		t.Fatal(err)
	}

	_, result, err := runOwnedAMQSend(ownedAMQSendOptions{Invocation: boundary}, amqCommandRequest{Arg: []string{"send"}})
	if err != nil || result.MessageID != "msg-existing" || result.Invoked || !result.Reconciled {
		t.Fatalf("err=%v result=%+v", err, result)
	}
}

func TestRunOwnedAMQSendRejectsSuccessfulOutputWithoutStableID(t *testing.T) {
	previous := runAMQCommand
	t.Cleanup(func() { runAMQCommand = previous })
	runAMQCommand = func(amqCommandRequest) ([]byte, error) {
		return []byte("accepted\n"), nil
	}

	_, result, err := runOwnedAMQSend(ownedAMQSendOptions{}, amqCommandRequest{Arg: []string{"send"}})
	if err == nil || !strings.Contains(err.Error(), "without a parseable stable message id") || !result.Invoked || result.MessageID != "" {
		t.Fatalf("err=%v result=%+v", err, result)
	}
}
