package main

import (
	"context"
	"log"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/ec2rolecreds"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/osmitickets-stack/osmi-server/internal/database"
	"github.com/osmitickets-stack/osmi-server/internal/infrastructure/email"
)

const (
	region          = "us-east-1"
	queueWait       = 20
	queueVisibility = 90
	receiveBeat     = "/tmp/osmi-email-receive.heartbeat"
	dispatchBeat    = "/tmp/osmi-email-dispatch.heartbeat"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--health" {
		if heartbeatFresh(receiveBeat) && heartbeatFresh(dispatchBeat) {
			return
		}
		os.Exit(1)
	}
	queueURL := strings.TrimSpace(os.Getenv("SES_EVENTS_QUEUE_URL"))
	if queueURL == "" || os.Getenv("SES_CONFIGURATION_SET") != "osmi-transactional-prod" {
		log.Fatal("email worker queue or SES configuration set is missing")
	}
	if os.Getenv("SMTP_FROM") != "boletos@myosmi.com" {
		log.Fatal("email worker sender configuration is incomplete")
	}
	if err := database.Init(); err != nil {
		log.Fatalf("email worker database unavailable: %T", err)
	}
	defer database.Close()
	ctx := context.Background()
	roleProvider := ec2rolecreds.New(func(options *ec2rolecreds.Options) {
		options.Client = imds.New(imds.Options{EnableFallback: aws.FalseTernary})
	})
	awsConfig, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(aws.NewCredentialsCache(roleProvider)),
	)
	if err != nil {
		log.Fatalf("email worker AWS configuration unavailable: %T", err)
	}
	if _, err := awsConfig.Credentials.Retrieve(ctx); err != nil {
		log.Fatalf("email worker EC2 role credentials unavailable: %T", err)
	}
	queue := sqs.NewFromConfig(awsConfig)
	outbox := email.NewTicketEmailOutbox(database.Pool)
	sender := email.NewSESAPIClient(sesv2.NewFromConfig(awsConfig))
	if err := sender.ValidateConfig(); err != nil {
		log.Fatal("email worker SES API configuration is invalid")
	}
	dispatcher := email.NewTicketEmailDispatcher(database.Pool, sender)
	go dispatchLoop(ctx, outbox, dispatcher)
	for {
		if err := receiveEvents(ctx, queue, queueURL, outbox); err != nil {
			log.Printf("SES event receive failed: %T", err)
			time.Sleep(5 * time.Second)
		} else {
			writeHeartbeat(receiveBeat)
		}
	}
}

func writeHeartbeat(path string) {
	if err := os.WriteFile(path, []byte("ok"), 0600); err != nil {
		log.Printf("email worker heartbeat failed: %T", err)
	}
}

func heartbeatFresh(path string) bool {
	stat, err := os.Stat(path)
	return err == nil && time.Since(stat.ModTime()) < 90*time.Second
}

func dispatchLoop(ctx context.Context, outbox *email.TicketEmailOutbox, dispatcher *email.TicketEmailDispatcher) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		runCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		maintenanceErr := outbox.ExhaustExpiredFinalLeases(runCtx)
		if maintenanceErr != nil {
			log.Printf("ticket email lease maintenance failed: %T", maintenanceErr)
		}
		for i := 0; i < 10; i++ {
			processed, err := dispatcher.ProcessOne(runCtx)
			if err != nil {
				log.Printf("ticket email dispatch: %v", err)
			}
			if !processed {
				break
			}
		}
		cancel()
		if maintenanceErr == nil {
			writeHeartbeat(dispatchBeat)
		}
		<-ticker.C
	}
}

type feedbackQueue interface {
	ReceiveMessage(context.Context, *sqs.ReceiveMessageInput, ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(context.Context, *sqs.DeleteMessageInput, ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
}

type eventRecorder interface {
	RecordSESEvent(context.Context, *email.SESEvent) error
}

var _ feedbackQueue = (*sqs.Client)(nil)
var _ eventRecorder = (*email.TicketEmailOutbox)(nil)

func receiveEvents(ctx context.Context, queue feedbackQueue, queueURL string, outbox eventRecorder) error {
	receiveCtx, receiveCancel := context.WithTimeout(ctx, 30*time.Second)
	result, err := queue.ReceiveMessage(receiveCtx, &sqs.ReceiveMessageInput{
		QueueUrl: aws.String(queueURL), MaxNumberOfMessages: 10,
		WaitTimeSeconds: queueWait, VisibilityTimeout: queueVisibility,
	})
	receiveCancel()
	if err != nil {
		return err
	}
	for _, message := range result.Messages {
		if message.Body == nil || message.ReceiptHandle == nil {
			continue
		}
		event, err := email.DecodeSESEvent([]byte(*message.Body))
		if err == nil {
			applyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			err = outbox.RecordSESEvent(applyCtx, event)
			cancel()
		}
		if err != nil {
			// Preserve unknown, invalid, and failed events for SQS retry and its DLQ.
			log.Printf("SES event processing failed: %T", err)
			continue
		}
		deleteCtx, deleteCancel := context.WithTimeout(ctx, 10*time.Second)
		_, err = queue.DeleteMessage(deleteCtx, &sqs.DeleteMessageInput{
			QueueUrl: aws.String(queueURL), ReceiptHandle: message.ReceiptHandle,
		})
		deleteCancel()
		if err != nil {
			log.Printf("SES event delete failed: %T", err)
		}
	}
	return nil
}
