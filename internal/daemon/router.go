package daemon

import (
	"context"
	"errors"
	"os"

	"github.com/Vadym903/Intenter/internal/ipc"
	"github.com/Vadym903/Intenter/internal/storage"
)

// registerHandlers wires every protocol method onto the server (§10.4).
func (d *Daemon) registerHandlers() {
	d.server.Handle(ipc.MethodPing, d.handlePing)
	d.server.Handle(ipc.MethodStatus, d.handleStatus)
	d.server.Handle(ipc.MethodShutdown, d.handleShutdown)

	d.server.Handle(ipc.MethodEvaluate, d.handleEvaluate)
	d.server.Handle(ipc.MethodRecordPrompt, d.handleRecordPrompt)
	d.server.Handle(ipc.MethodRecordAdapterAction, d.handleRecordAdapterAction)
	d.server.Handle(ipc.MethodReportExecution, d.handleReportExecution)
	d.server.Handle(ipc.MethodAgentConfigChanged, d.handleAgentConfigChanged)

	d.server.Handle(ipc.MethodListApprovals, d.handleListApprovals)
	d.server.Handle(ipc.MethodGetApproval, d.handleGetApproval)
	d.server.Handle(ipc.MethodSetApprovalState, d.handleSetApprovalState)
	d.server.Handle(ipc.MethodCreateApproval, d.handleCreateApproval)

	d.server.Handle(ipc.MethodListHistory, d.handleListHistory)
	d.server.Handle(ipc.MethodGetHistoryEvent, d.handleGetHistoryEvent)
	d.server.Handle(ipc.MethodSummarize, d.handleSummarize)
}

// decodeParams decodes request parameters, mapping failures to BAD_REQUEST.
func decodeParams[T any](req *ipc.Request) (T, error) {
	var params T
	if err := req.DecodeParams(&params); err != nil {
		return params, ipc.Errorf(ipc.CodeBadRequest, "%s", err.Error())
	}
	return params, nil
}

// storageError maps repository errors onto protocol error codes.
func storageError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, storage.ErrNotFound) {
		return ipc.Errorf(ipc.CodeNotFound, "%s", err.Error())
	}
	return err
}

func currentPID() int { return os.Getpid() }

// ctxOrBackground guards against a nil context reaching the repositories.
func ctxOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
