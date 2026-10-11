package ingest

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/aws/aws-lambda-go/events"
	"github.com/google/uuid"
	"github.com/vaultops/core/internal/store"
)

// PG adapts *store.Store to the Store interface.
type PG struct{ S *store.Store }

func (p PG) OrganizationByWebhookToken(ctx context.Context, token string) (store.Organization, error) {
	return p.S.OrganizationByWebhookToken(ctx, token)
}

func (p PG) InOrg(ctx context.Context, org uuid.UUID, fn func(OrgTx) error) error {
	return p.S.InOrg(ctx, org, func(tx *store.Tx) error { return fn(tx) })
}

// HandleSQS is the Lambda entry point. It reports per-message failures so a
// single bad delivery does not force the whole batch to be retried.
func (p *Processor) HandleSQS(ctx context.Context, ev events.SQSEvent) (events.SQSEventResponse, error) {
	var resp events.SQSEventResponse
	for _, r := range ev.Records {
		var m Message
		if err := json.Unmarshal([]byte(r.Body), &m); err != nil {
			p.Log.Warn("dropping undecodable SQS message", "message_id", r.MessageId)
			continue
		}
		count, _ := strconv.Atoi(r.Attributes["ApproximateReceiveCount"])
		if err := p.Handle(ctx, m, max(count, 1)); err != nil {
			p.Log.Error("delivery failed; will retry", "message_id", r.MessageId, "err", err)
			resp.BatchItemFailures = append(resp.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: r.MessageId})
		}
	}
	return resp, nil
}
