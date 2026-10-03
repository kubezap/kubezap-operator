package awsmessaging

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

// SQSAPI is the subset of the SQS client the subscriber uses. *sqs.Client
// satisfies it; unit tests substitute a fake.
type SQSAPI interface {
	ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(ctx context.Context, in *sqs.DeleteMessageInput, optFns ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
	GetQueueAttributes(ctx context.Context, in *sqs.GetQueueAttributesInput, optFns ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error)
}

var _ SQSAPI = (*sqs.Client)(nil)

// NewAWSConfig builds the shared aws.Config (static credentials from the
// environment-injected Secret values, region, optional endpoint override).
// It is shared by the SQS subscriber and, in STORY-073, the SNS publisher.
// IRSA / workload identity is out of scope for the pilot.
func NewAWSConfig(ctx context.Context, c Config) (aws.Config, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(c.Region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, c.SessionToken)),
	)
	if err != nil {
		return aws.Config{}, fmt.Errorf("loading AWS config: %w", err)
	}
	return cfg, nil
}

// NewSQSClient returns an SQS client honoring Config.EndpointURL (LocalStack).
func NewSQSClient(ctx context.Context, c Config) (*sqs.Client, error) {
	cfg, err := NewAWSConfig(ctx, c)
	if err != nil {
		return nil, err
	}
	return sqs.NewFromConfig(cfg, func(o *sqs.Options) {
		if c.EndpointURL != "" {
			o.BaseEndpoint = aws.String(c.EndpointURL)
		}
	}), nil
}
