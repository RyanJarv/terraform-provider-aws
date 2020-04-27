package aws

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/route53"
)

func resourceAwsRoute53ZoneAssociation() *schema.Resource {
	return &schema.Resource{
		Create: resourceAwsRoute53ZoneAssociationCreate,
		Read:   resourceAwsRoute53ZoneAssociationRead,
		Delete: resourceAwsRoute53ZoneAssociationDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},

		Schema: map[string]*schema.Schema{
			"zone_id": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},

			"vpc_id": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},

			"vpc_region": {
				Type:     schema.TypeString,
				Optional: true,
				Computed: true,
				ForceNew: true,
			},

			"cross_account": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
				ForceNew: true,
			},
		},
	}
}

func resourceAwsRoute53ZoneAssociationCreate(d *schema.ResourceData, meta interface{}) error {
	r53 := meta.(*AWSClient).r53conn

	req := &route53.AssociateVPCWithHostedZoneInput{
		HostedZoneId: aws.String(d.Get("zone_id").(string)),
		VPC: &route53.VPC{
			VPCId:     aws.String(d.Get("vpc_id").(string)),
			VPCRegion: aws.String(meta.(*AWSClient).region),
		},
		Comment: aws.String("Managed by Terraform"),
	}
	if w := d.Get("vpc_region"); w != "" {
		req.VPC.VPCRegion = aws.String(w.(string))
	}

	log.Printf("[DEBUG] Associating Route53 Private Zone %s with VPC %s with region %s", *req.HostedZoneId, *req.VPC.VPCId, *req.VPC.VPCRegion)

	zone_id := d.Get("zone_id").(string)
	vpc_id := d.Get("vpc_id").(string)

	// Ignore errors that indicate we're already associated with this hosted zone. We will use this to determine the
	// current state of the account when we are associating with a hosted zone in another account.
	//
	// If the Route53 API ever add's the ability to determine current associations of an account then this should be
	// replaced to use that.
	exists := fmt.Sprintf(`ConflictingDomainExists: The VPC %s .* associated with the hosted zone %s .*`, vpc_id, zone_id)

	resp, apiErr := r53.AssociateVPCWithHostedZone(req)
	if apiErr != nil {
		// Ignore already associated errors if the hosted zone isn't in our account
		if d.Get("cross_account").(bool) {
			match, compileErr := regexp.Match(exists, []byte(apiErr.Error()))
			if compileErr != nil {
				return compileErr
			} else if !match {
				return apiErr
			}
		} else {
			return apiErr
		}
	}

	// Store association id
	d.SetId(fmt.Sprintf("%s:%s", *req.HostedZoneId, *req.VPC.VPCId))

	var refreshFunc func() (result interface{}, state string, err error)

	if d.Get("cross_account").(bool) {
		// In a cross a
		refreshFunc = func() (result interface{}, state string, err error) {
			_, apiErr := r53.AssociateVPCWithHostedZone(req)

			if apiErr == nil {
				// call succeeded because we're associated yet
				return true, "PENDING", nil
			} else {
				if match, compileErr := regexp.Match(exists, []byte(apiErr.Error())); match {
					if compileErr != nil {
						return nil, "UNKNOWN", compileErr
					}
					// call errored because we're associated
					return true, "INSYNC", nil
				}
			}

			// call errored for some other reason
			return nil, "UNKNOWN", apiErr
		}
	} else {
		// Wait until we are done initializing
		refreshFunc = func() (result interface{}, state string, err error) {
			changeRequest := &route53.GetChangeInput{
				Id: aws.String(cleanChangeID(*resp.ChangeInfo.Id)),
			}
			return resourceAwsGoRoute53Wait(r53, changeRequest)
		}
	}

	wait := resource.StateChangeConf{
		Delay:      30 * time.Second,
		Pending:    []string{"PENDING"},
		Target:     []string{"INSYNC"},
		Timeout:    10 * time.Minute,
		MinTimeout: 2 * time.Second,
		Refresh:    refreshFunc,
	}
	_, err := wait.WaitForState()
	if err != nil {
		return err
	}

	return resourceAwsRoute53ZoneAssociationRead(d, meta)
}

func resourceAwsRoute53ZoneAssociationRead(d *schema.ResourceData, meta interface{}) error {
	zoneID, vpcID, err := resourceAwsRoute53ZoneAssociationParseId(d.Id())
	if err != nil {
		return err
	}

	d.Set("vpc_id", vpcID)
	//d.Set("vpc_region", vpc.VPCRegion)
	d.Set("zone_id", zoneID)

	return nil
}

func resourceAwsRoute53ZoneAssociationDelete(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).r53conn

	zoneID, vpcID, err := resourceAwsRoute53ZoneAssociationParseId(d.Id())

	if err != nil {
		return err
	}

	log.Printf("[DEBUG] Disassociating Route 53 Hosted Zone (%s) Association: %s", zoneID, vpcID)

	req := &route53.DisassociateVPCFromHostedZoneInput{
		HostedZoneId: aws.String(zoneID),
		VPC: &route53.VPC{
			VPCId:     aws.String(vpcID),
			VPCRegion: aws.String(d.Get("vpc_region").(string)),
		},
		Comment: aws.String("Managed by Terraform"),
	}

	_, err = conn.DisassociateVPCFromHostedZone(req)

	if err != nil {
		return fmt.Errorf("error disassociating Route 53 Hosted Zone (%s) Association (%s): %s", zoneID, vpcID, err)
	}

	return nil
}

func resourceAwsRoute53ZoneAssociationParseId(id string) (string, string, error) {
	parts := strings.Split(id, ":")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("Unexpected format of ID (%q), expected ZONEID:VPCID", id)
	}
	return parts[0], parts[1], nil
}

func route53GetZoneAssociation(conn *route53.Route53, zoneID, vpcID string) (*route53.VPC, error) {
	input := &route53.GetHostedZoneInput{
		Id: aws.String(zoneID),
	}

	output, err := conn.GetHostedZone(input)

	if err != nil {
		return nil, err
	}

	var vpc *route53.VPC
	for _, zoneVPC := range output.VPCs {
		if vpcID == aws.StringValue(zoneVPC.VPCId) {
			vpc = zoneVPC
			break
		}
	}

	return vpc, nil
}
