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
)

func main() {
	var instanceID, logGroup string

	flag.StringVar(&instanceID, "i", "", "EC2 Instance ID")
	flag.StringVar(&instanceID, "instance-id", "", "EC2 Instance ID")
	flag.StringVar(&logGroup, "l", "", "Cloudwatch Logs Log Group")
	flag.StringVar(&logGroup, "log-group", "", "Cloudwatch Logs Log Group")
	flag.Usage = func() {
		fmt.Print(`Spy on DNS requests for your EC2 instances in (almost) real-time

usage: ec2-dnsspy [options]
	-i, --instance-id	EC2 Instance ID
	-l, --log-group		Cloudwatch Logs Log Group
`)
	}
	flag.Parse()

	// TODO (check flags are set)

	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		panic("couldn't configure aws client")
	}
	client := cloudwatchlogs.NewFromConfig(cfg)

	tailConfig := TailConfig{
		LogGroupName:  aws.String(logGroup),
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

	tail, err := Tail(client, tailConfig, limiter, log.New(ioutil.Discard, "", 0))
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
