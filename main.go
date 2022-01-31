package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/route53resolver"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53resolver/types"
)

// TODO(dw): Make a DNSSpy struct to encapsulate this stuff?

const (
	DEFAULT_LOG_GROUP_NAME          = "/ec2/dnsspy"
	DEFAULT_RESOLVER_QUERY_LOG_NAME = "ec2-dnsspy"
)

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
	if err != nil {
		return aws.String(""), err
	}

	return getLogGroupArn(ctx, cwl, name)
}

func ensureResolverQueryLogExists(ctx context.Context, r53 *route53resolver.Client, resolverQueryLogName, logGroupArn, vpcID *string) error {
	// TODO handle if there are > 1 or something, multi region?
	//      it might be better to just check for this w/ an associated vpc id before ensuring the cw logs?
	list, err := r53.ListResolverQueryLogConfigs(ctx, &route53resolver.ListResolverQueryLogConfigsInput{
		//
		Filters: []r53types.Filter{
			{
				Name:   aws.String("Name"),
				Values: []string{*resolverQueryLogName},
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
		CreatorRequestId: aws.String("TODO-thisshouldbeunique"),
		DestinationArn:   logGroupArn,
		Name:             aws.String(*resolverQueryLogName),
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

func setup(ctx context.Context, cfg aws.Config, instanceID, logGroupName, resolverQueryLogName string) error {
	fmt.Fprintln(os.Stderr, "Looking up VPC ID")
	ec2c := ec2.NewFromConfig(cfg)
	resp, err := ec2c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return err
	}
	if len(resp.Reservations) == 0 {
		return fmt.Errorf("Instance %s is not active in a VPC", instanceID)
	}
	vpcID := resp.Reservations[0].Instances[0].VpcId

	fmt.Fprintln(os.Stderr, "Creating Cloudwatch log group")
	cw := cloudwatchlogs.NewFromConfig(cfg)
	logGroupArn, err := ensureLogGroupExists(ctx, cw, logGroupName, 1)
	if err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "Creating Route53 resolver query log")
	r53 := route53resolver.NewFromConfig(cfg)
	err = ensureResolverQueryLogExists(ctx, r53, &resolverQueryLogName, logGroupArn, vpcID)
	if err != nil {
		return err
	}

	return nil
}

func teardown(ctx context.Context, cfg aws.Config, logGroupName, vpcID, resolverQueryLogID string) error {
	r53 := route53resolver.NewFromConfig(cfg)
	_, err := r53.DisassociateResolverQueryLogConfig(ctx, &route53resolver.DisassociateResolverQueryLogConfigInput{
		ResolverQueryLogConfigId: &resolverQueryLogID,
		ResourceId:               &vpcID,
	})
	if err != nil {
		return err
	}
	_, err = r53.DeleteResolverQueryLogConfig(ctx, &route53resolver.DeleteResolverQueryLogConfigInput{
		ResolverQueryLogConfigId: &resolverQueryLogID,
	})
	if err != nil {
		return err
	}
	cw := cloudwatchlogs.NewFromConfig(cfg)
	_, err = cw.DeleteLogGroup(ctx, &cloudwatchlogs.DeleteLogGroupInput{
		LogGroupName: &logGroupName,
	})
	if err != nil {
		return err
	}
	return nil
}

func main() {
	var instanceID, logGroupName, resolverQueryLogName, outputFmt string
	var rm bool

	flag.StringVar(&instanceID, "i", "", "EC2 Instance ID")
	flag.StringVar(&instanceID, "instance-id", "", "EC2 Instance ID")
	flag.StringVar(&logGroupName, "l", DEFAULT_LOG_GROUP_NAME, "Cloudwatch log group name to log DNS requests")
	flag.StringVar(&logGroupName, "log-group-name", DEFAULT_LOG_GROUP_NAME, "Cloudwatch log group name to log DNS requests")
	flag.StringVar(&resolverQueryLogName, "r", DEFAULT_RESOLVER_QUERY_LOG_NAME, "Name to give the Route53 Resolver query log")
	flag.StringVar(&resolverQueryLogName, "resolver-query-log-name", DEFAULT_RESOLVER_QUERY_LOG_NAME, "Name to give the Route53 Resolver query log")
	flag.StringVar(&outputFmt, "o", "default", "Output format. 'default' or 'json'")
	flag.StringVar(&outputFmt, "output", "default", "Output format. 'default' or 'json'")
	flag.BoolVar(&rm, "rm", false, "Remove ec2-dnsspy related AWS resources when shutting down")

	flag.Usage = func() {
		fmt.Print(`Spy on DNS requests for your EC2 instances in (almost) real-time

usage: ec2-dnsspy [options]
	-i, --instance-id               EC2 Instance ID
	-l, --log-group-name            Cloudwatch log group name to log DNS requests (Default: /ec2/dnsspy)
	-r, --resolver-query-log-name   Name to give the Route53 Resolver query log (Default: ec2-dnsspy)
	--rm                            Remove ec2-dnsspy related AWS resources when shutting down

	-o, --output                    Output format. 'default' or 'json'
`)
	}
	flag.Parse()

	if instanceID == "" {
		flag.Usage()
		fmt.Println("Missing instance ID")
		os.Exit(1)
	}

	if outputFmt != "default" && outputFmt != "json" {
		flag.Usage()
		fmt.Printf("Invalid output type: %s\n", outputFmt)
		os.Exit(1)
	}

	ctx := context.TODO()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		panic("couldn't configure aws client")
	}

	err = setup(ctx, cfg, instanceID, logGroupName, resolverQueryLogName)
	if err != nil {
		panic(err)
	}
	cw := cloudwatchlogs.NewFromConfig(cfg)

	tailConfig := TailConfig{
		LogGroupName:  aws.String(logGroupName),
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

	if outputFmt == "default" {
		fmt.Printf("%-5s %-45s %-14s\n", "query", "name", "timestamp")
	}

	for event := range tail {
		if outputFmt == "default" {
			var r DNSQuery
			err = json.Unmarshal([]byte(*event.Message), &r)
			if err != nil {
				fmt.Println(*event.Message)
				continue
			}
			fmt.Printf("%-5s %-45s %-14s\n", r.QueryType, r.QueryName, r.QueryTimestamp)
		} else if outputFmt == "json" {
			fmt.Println(*event.Message)
		}
	}
}
