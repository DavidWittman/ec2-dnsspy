package main

type DNSQuery struct {
	Version        string `json:"version"`
	AccountId      string `json:"account_id"`
	Region         string `json:"region"`
	VpcID          string `json:"vpc_id"`
	QueryTimestamp string `json:"query_timestamp"`
	QueryName      string `json:"query_name"`
	QueryType      string `json:"query_type"`
	QueryClass     string `json:"query_class"`
	Rcode          string `json:"rcode"`
	Answers        []struct {
		Rdata string
		Type  string
		Class string
	} `json:"answers"`
	SrcAddr   string `json:"srcaddr"`
	SrcPort   string `json:"srcport"`
	Transport string `json:"transport"`
	SrcIDs    struct {
		Instance string `json:"instance"`
	} `json:"srcids"`
}
