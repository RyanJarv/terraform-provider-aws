package aws

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/terraform"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/route53"
)

func TestAccAWSRoute53VpcAssociationAuthorization_basic(t *testing.T) {
	var providers []*schema.Provider

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:          func() { testAccPreCheck(t) },
		ProviderFactories: testAccProviderFactories(&providers),
		CheckDestroy:      testAccCheckRoute53VPCAssociationAuthorizationDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccRoute53VPCAssociationAuthorizationConfig(),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoute53VPCAssociationAuthorizationExists("aws_route53_vpc_association_authorization.test"),
				),
			},
		},
	})
}

func testAccCheckRoute53VPCAssociationAuthorizationDestroy(s *terraform.State) error {
	conn := testAccProvider.Meta().(*AWSClient).r53conn

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "aws_route53_vpc_association_authorization" {
			continue
		}

		zone_id, vpc_id, err := resourceAwsRoute53ZoneAssociationParseId(rs.Primary.ID)
		if err != nil {
			return err
		}

		req := route53.ListVPCAssociationAuthorizationsInput{
			HostedZoneId: aws.String(zone_id),
		}

		res, err := conn.ListVPCAssociationAuthorizations(&req)
		if isAWSErr(err, route53.ErrCodeNoSuchHostedZone, "") {
			return nil
		}
		if err != nil {
			return err
		}

		for _, vpc := range res.VPCs {
			if vpc_id == *vpc.VPCId {
				return fmt.Errorf("VPC association authorization for zone %v with %v still exists", zone_id, vpc_id)
			}
		}
	}
	return nil
}

func testAccCheckRoute53VPCAssociationAuthorizationExists(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Not found: %s", n)
		}

		if rs.Primary.ID == "" {
			return fmt.Errorf("No VPC association authorization ID is set")
		}

		zone_id, vpc_id, err := resourceAwsRoute53ZoneAssociationParseId(rs.Primary.ID)
		if err != nil {
			return err
		}

		conn := testAccProvider.Meta().(*AWSClient).r53conn

		req := route53.ListVPCAssociationAuthorizationsInput{
			HostedZoneId: aws.String(zone_id),
		}

		res, err := conn.ListVPCAssociationAuthorizations(&req)
		if err != nil {
			return err
		}

		for _, vpc := range res.VPCs {
			if vpc_id == *vpc.VPCId {
				return nil
			}
		}

		return fmt.Errorf("VPC association authorization not found")
	}
}

func testAccRoute53VPCAssociationAuthorizationConfig() string {
	return testAccAlternateAccountProviderConfig() + `
resource "aws_vpc" "test" {
  cidr_block           = "10.6.0.0/16"
  enable_dns_hostnames = true
  enable_dns_support   = true
}

resource "aws_route53_zone" "test" {
  name = "example.com"
  vpc {
    vpc_id = "${aws_vpc.test.id}"
  }
}

resource "aws_vpc" "alternate" {
  provider             = "aws.alternate"
  cidr_block           = "10.7.0.0/16"
  enable_dns_hostnames = true
  enable_dns_support   = true
}

resource "aws_route53_vpc_association_authorization" "test" {
  zone_id = "${aws_route53_zone.test.id}"
  vpc_id  = "${aws_vpc.alternate.id}"
}
`
}
