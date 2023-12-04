package aws

import (
	"context"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/elbv2"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"strings"
)

type client struct {
	Elb        elbv2.ELBV2
	Ec2Client  *ec2.EC2
	VPC        string
	protocol   string
	actionType string
}

func (c client) DeleteListenerAndTargetArn(listenerArn string, targetArn string) error {
	_, err := c.Elb.DeleteListener(&elbv2.DeleteListenerInput{ListenerArn: aws.String(listenerArn)})
	if err != nil {
		return err
	}
	_, err = c.Elb.DeleteTargetGroup(&elbv2.DeleteTargetGroupInput{TargetGroupArn: aws.String(targetArn)})
	if err != nil {
		return err
	}
	return nil
}

func (c client) CheckListener(
	_ context.Context,
	svcListenerArn string,
	svcTargetGroupArn string,
	_ string,
	svcNLBPort int,
	svcNodePort int,
) error {
	// TODO: add NLB check
	listeners, err := c.Elb.DescribeListeners(&elbv2.DescribeListenersInput{
		ListenerArns: []*string{aws.String(svcListenerArn)},
		PageSize:     aws.Int64(50),
	})
	if err != nil {
		return err
	}
	if *listeners.Listeners[0].Port != int64(svcNLBPort) {
		return errors.New("aws: listener port and svcNLBPort dont match")
	}

	targetGroupArn := listeners.Listeners[0].DefaultActions[0].ForwardConfig.TargetGroups[0].TargetGroupArn
	if *targetGroupArn != svcTargetGroupArn {
		return errors.New("aws: target group arn dont match")
	}

	groups, err := c.Elb.DescribeTargetGroups(&elbv2.DescribeTargetGroupsInput{
		LoadBalancerArn: nil,
		Marker:          nil,
		Names:           nil,
		PageSize:        nil,
		TargetGroupArns: []*string{targetGroupArn},
	})
	if err != nil {
		return err
	}
	if *groups.TargetGroups[0].Port != int64(svcNodePort) {
		return errors.New("aws: target port and node port dont match")
	}
	return nil
}

func (c client) CreateNLBListenerForPort(
	nlbName string,
	port int,
	nodePort int,
	svcName string,
) (string, string, error) {
	svcName = strings.Replace(svcName, "/", "-", 1)

	nlbList, err := c.Elb.DescribeLoadBalancers(&elbv2.DescribeLoadBalancersInput{Names: []*string{&nlbName}})
	if err != nil {
		return "", "", err
	}
	if len(nlbList.LoadBalancers) != 1 {
		return "", "", errors.New(fmt.Sprintf("aws: %s nlb not found", nlbName))
	}

	log.Log.Info("aws: nlb found")
	nlb := nlbList.LoadBalancers[0]

	targetGroupArn, err := c.GetTargetGroupArn(c.VPC, int64(nodePort))
	if err != nil {
		return "", "", err
	}
	log.Log.Info("aws: target group found")

	listener, err := c.Elb.CreateListener(&elbv2.CreateListenerInput{
		DefaultActions: []*elbv2.Action{
			{
				TargetGroupArn: aws.String(targetGroupArn),
				Type:           aws.String(c.actionType),
			},
		},
		LoadBalancerArn: nlb.LoadBalancerArn,
		Port:            aws.Int64(int64(port)),
		Protocol:        &c.protocol,
	})
	if err != nil {
		return "", "", err
	}
	log.Log.Info("aws: listener created")
	return *listener.Listeners[0].ListenerArn, targetGroupArn, nil
}

func (c client) GetTargetGroupArn(vpcId string, nodePort int64) (string, error) {
	pageSize := int64(50)
	targetGroupName := fmt.Sprintf("%d", nodePort)
	groups, err := c.Elb.DescribeTargetGroups(&elbv2.DescribeTargetGroupsInput{
		Names:    []*string{&targetGroupName},
		PageSize: &pageSize,
	})
	if err != nil {
		if !strings.Contains(err.Error(), "TargetGroupNotFound") {
			return "", err
		}
	}
	if len(groups.TargetGroups) == 1 {
		return *groups.TargetGroups[0].TargetGroupArn, nil
	}

	if len(groups.TargetGroups) == 0 {
		group, err := c.Elb.CreateTargetGroup(&elbv2.CreateTargetGroupInput{
			Name:       aws.String(targetGroupName),
			Port:       aws.Int64(nodePort),
			Protocol:   aws.String(elbv2.ProtocolEnumTcp),
			TargetType: aws.String(elbv2.TargetTypeEnumInstance),
			VpcId:      aws.String(vpcId),
		})
		if err != nil {
			return "", err
		}
		instances, err := c.Ec2Client.DescribeInstances(&ec2.DescribeInstancesInput{
			Filters: []*ec2.Filter{
				&ec2.Filter{
					Name: aws.String("vpc-id"),
					Values: []*string{
						aws.String(c.VPC),
					},
				},
			},
		})
		if err != nil {
			return "", err
		}
		targetDescs := []*elbv2.TargetDescription{}
		for _, i := range instances.Reservations[0].Instances {
			targetDescs = append(targetDescs, &elbv2.TargetDescription{
				Id:   i.InstanceId,
				Port: aws.Int64(nodePort),
			})
		}
		_, err = c.Elb.RegisterTargets(&elbv2.RegisterTargetsInput{
			TargetGroupArn: group.TargetGroups[0].TargetGroupArn,
			Targets:        targetDescs,
		})
		if err != nil {
			return "", err
		}
		return *group.TargetGroups[0].TargetGroupArn, nil
	}
	return "", errors.New("aws: TargetGroup not found")
}

func New(_ context.Context) Client {
	s := session.Must(session.NewSession())
	s.Config.Region = aws.String("us-west-1")
	var in *ec2.EC2
	in = ec2.New(s)

	return &client{
		Elb:        *elbv2.New(s),
		VPC:        os.Getenv("VPC_ID"),
		Ec2Client:  in,
		protocol:   "TCP",
		actionType: elbv2.ActionTypeEnumForward,
	}
}

type Client interface {
	CreateNLBListenerForPort(
		nlb string,
		port int,
		nodePort int,
		svcName string,
	) (string, string, error)
	CheckListener(
		ctx context.Context,
		listenerArn string,
		targetArn string,
		nlb string,
		exposedPort int,
		nodePort int,
	) error
	DeleteListenerAndTargetArn(listenerArn string, targetArn string) error
}
