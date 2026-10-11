// Package localsqs feeds an SQS-compatible queue (ElasticMQ) into the same
// Lambda SQS handler, so local runs exercise real retry and DLQ semantics.
// Local development only.
package localsqs

import (
	"context"
	"log/slog"
	"net/url"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

type Handler func(context.Context, events.SQSEvent) (events.SQSEventResponse, error)

// Run polls queueURL until ctx is cancelled.
func Run(ctx context.Context, queueURL string, h Handler, log *slog.Logger) error {
	u, err := url.Parse(queueURL)
	if err != nil {
		return err
	}
	endpoint := u.Scheme + "://" + u.Host
	client := sqs.New(sqs.Options{
		Region:       "ap-northeast-1",
		BaseEndpoint: aws.String(endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider("local", "local", ""),
	})
	log.Info("local SQS poller started", "queue", queueURL)
	for ctx.Err() == nil {
		out, err := client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:                    aws.String(queueURL),
			MaxNumberOfMessages:         10,
			WaitTimeSeconds:             1,
			MessageSystemAttributeNames: []types.MessageSystemAttributeName{types.MessageSystemAttributeNameApproximateReceiveCount},
		})
		if err != nil {
			log.Warn("receive failed", "err", err)
			time.Sleep(time.Second)
			continue
		}
		if len(out.Messages) == 0 {
			continue
		}
		ev := events.SQSEvent{}
		receipts := map[string]string{}
		for _, m := range out.Messages {
			attrs := map[string]string{}
			for k, v := range m.Attributes {
				attrs[k] = v
			}
			ev.Records = append(ev.Records, events.SQSMessage{MessageId: aws.ToString(m.MessageId), Body: aws.ToString(m.Body), Attributes: attrs})
			receipts[aws.ToString(m.MessageId)] = aws.ToString(m.ReceiptHandle)
		}
		resp, err := h(ctx, ev)
		if err != nil {
			log.Error("handler error; batch will be redelivered", "err", err)
			continue
		}
		failed := map[string]bool{}
		for _, f := range resp.BatchItemFailures {
			failed[f.ItemIdentifier] = true
		}
		for id, rh := range receipts {
			if failed[id] {
				continue // stays invisible until the visibility timeout, then redelivered
			}
			if _, err := client.DeleteMessage(ctx, &sqs.DeleteMessageInput{QueueUrl: aws.String(queueURL), ReceiptHandle: aws.String(rh)}); err != nil {
				log.Warn("delete failed", "err", err)
			}
		}
	}
	return ctx.Err()
}
