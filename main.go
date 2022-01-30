package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/route53resolver"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53resolver/types"
)

const LOG_GROUP_NAME = "/ec2/dnsspy"

func getLogGroupArn(ctx context.Context, cwl *cloudwatchlogs.Client, name string) (*string, error) {
	resp, err := cwl.DescribeLogGroups(ctx, &cloudwatchlogs.DescribeLogGroupsInput{
		Limit:              aws.Int32(1),
		LogGroupNamePrefix: &name,
	})
	if err != nil {
		return aws.String(""), err
	}

	if len(resp.LogGroups) > 0 {
		return resp.LogGroups[0].Arn, nil
	}

	return nil, nil
}

// ensureLogGroupExists first checks if the log group exists, if it doesn't it will create one.
// returns the ARN of the log group or an error
func ensureLogGroupExists(ctx context.Context, cwl *cloudwatchlogs.Client, name string, retentionDays int32) (*string, error) {
	arn, err := getLogGroupArn(ctx, cwl, name)
	if err != nil {
		return aws.String(""), err
	}
	if arn != nil {
		return arn, nil
	}

	_, err = cwl.CreateLogGroup(ctx, &cloudwatchlogs.CreateLogGroupInput{
		LogGroupName: &name,
	})
	if err != nil {
		return aws.String(""), err
	}

	_, err = cwl.PutRetentionPolicy(ctx, &cloudwatchlogs.PutRetentionPolicyInput{
		RetentionInDays: aws.Int32(retentionDays),
		LogGroupName:    &name,
	})

	return getLogGroupArn(ctx, cwl, name)
}

func ensureResolverQueryLogExists(ctx context.Context, r53 *route53resolver.Client, logGroupArn, vpcID *string) error {
	// TODO handle if there are > 1 or something, multi region?
	//      it might be better to just check for this w/ an associated vpc id before ensuring the cw logs?
	list, err := r53.ListResolverQueryLogConfigs(ctx, &route53resolver.ListResolverQueryLogConfigsInput{
		//
		Filters: []r53types.Filter{
			r53types.Filter{
				Name:   aws.String("Name"),
				Values: []string{"ec2-dnsspy"},
			},
		},
	})
	if err != nil {
		return err
	}
	// TODO handle > 1
	if list.TotalCount > 0 {
		return nil
	}
	resp, err := r53.CreateResolverQueryLogConfig(ctx, &route53resolver.CreateResolverQueryLogConfigInput{
		CreatorRequestId: aws.String("todothisshouldbeunique"),
		DestinationArn:   logGroupArn,
		Name:             aws.String("ec2-dnsspy"),
	})
	if err != nil {
		return err
	}
	_, err = r53.AssociateResolverQueryLogConfig(ctx, &route53resolver.AssociateResolverQueryLogConfigInput{
		ResolverQueryLogConfigId: resp.ResolverQueryLogConfig.Id,
		ResourceId:               vpcID,
	})
	return err
}

func setup(ctx context.Context, cfg aws.Config, instanceID string) error {
	fmt.Println("Looking up VPC ID")
	ec2c := ec2.NewFromConfig(cfg)
	resp, err := ec2c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return err
	}
	vpcID := resp.Reservations[0].Instances[0].VpcId

	fmt.Println("Creating Cloudwatch log group")
	cw := cloudwatchlogs.NewFromConfig(cfg)
	logGroupArn, err := ensureLogGroupExists(ctx, cw, LOG_GROUP_NAME, 1)
	if err != nil {
		return err
	}

	fmt.Println("Creating Route53 resolver query log")
	r53 := route53resolver.NewFromConfig(cfg)
	err = ensureResolverQueryLogExists(ctx, r53, logGroupArn, vpcID)
	if err != nil {
		return err
	}

	return nil
}

func main() {
	var instanceID string

	flag.StringVar(&instanceID, "i", "", "EC2 Instance ID")
	flag.StringVar(&instanceID, "instance-id", "", "EC2 Instance ID")
	flag.Usage = func() {
		fmt.Print(`Spy on DNS requests for your EC2 instances in (almost) real-time

usage: ec2-dnsspy [options]
	-i, --instance-id	EC2 Instance ID
`)
	}
	flag.Parse()

	ctx := context.TODO()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		panic("couldn't configure aws client")
	}

	err = setup(ctx, cfg, instanceID)
	if err != nil {
		panic(err)
	}
	cw := cloudwatchlogs.NewFromConfig(cfg)

	tailConfig := TailConfig{
		LogGroupName:  aws.String(LOG_GROUP_NAME),
		LogStreamName: aws.String("."),
		StartTime:     aws.Time(time.Now()),
		EndTime:       aws.Time(time.Now()),
		Follow:        aws.Bool(true),
		Grep:          aws.String(instanceID),
		Grepv:         aws.String(""),
	}

	limiter := make(chan time.Time, 1)
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		for range ticker.C {
			limiter <- time.Now()
		}
	}()

	tail, err := Tail(cw, tailConfig, limiter, log.New(ioutil.Discard, "", 0))
	if err != nil {
		panic(err)
	}

	fmt.Printf("%-5s %-30s %-14s\n", "query", "name", "timestamp")
	for event := range tail {
		var r DNSQuery
		err = json.Unmarshal([]byte(*event.Message), &r)
		if err != nil {
			fmt.Println(*event.Message)
			continue
		}
		fmt.Printf("%-5s %-30s %-14s\n", r.QueryType, r.QueryName, r.QueryTimestamp)
	}
}
