package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

type Store interface {
	AssignNLBAndPortToServiceInNamespace(
		ctx context.Context,
		nlb string,
		port int,
		serviceNamespacedName string,
		listenerArn string,
		targetArn string,
	) error
	GetVacantNLBAndPortForService(ctx context.Context, serviceNamespacedName string) (string, int, error)
	ReleaseNLBAndPortForService(ctx context.Context, serviceNamespacedName string, nlb string, port int)
	GetListenerArnFor(ctx context.Context, s string) string
	GetAllocationForSVC(ctx context.Context, name string) *Allocation
	GetNLBHost(nlb string) string
}

type Allocation struct {
	ListenerArn           string
	TargetArn             string
	NLB                   string
	Port                  int
	ServiceNamespacedName string
}

type typeNlbAllocationMap map[string]map[int]*string
type typeServiceAllocationMap map[string]*Allocation

type store struct {
	ServiceAllocationMap typeServiceAllocationMap
	NlbAllocationMap     typeNlbAllocationMap
	NlbHosts             map[string]string
}

func (s store) GetNLBHost(nlb string) string {
	return s.NlbHosts[nlb]
}

func (s store) GetAllocationForSVC(_ context.Context, name string) *Allocation {
	return s.ServiceAllocationMap[name]
}

func (s store) GetListenerArnFor(_ context.Context, serviceNamespacedName string) string {
	return s.ServiceAllocationMap[serviceNamespacedName].ListenerArn
}

func (s store) AssignNLBAndPortToServiceInNamespace(
	_ context.Context,
	nlb string,
	port int,
	serviceNamespacedName string,
	listenerArn string,
	targetArn string,
) error {
	if val, ok := s.NlbAllocationMap[nlb][port]; ok && *val != serviceNamespacedName {
		return fmt.Errorf("port reserved for svc %s", *s.NlbAllocationMap[nlb][port])
	}
	value := Allocation{
		ListenerArn:           listenerArn,
		TargetArn:             targetArn,
		NLB:                   nlb,
		Port:                  port,
		ServiceNamespacedName: serviceNamespacedName,
	}
	s.ServiceAllocationMap[serviceNamespacedName] = &value
	s.NlbAllocationMap[nlb][port] = &value.ServiceNamespacedName
	return nil
}

func (s store) ReleaseNLBAndPortForService(ctx context.Context, serviceNamespacedName string, nlb string, port int) {
	if val, ok := s.ServiceAllocationMap[serviceNamespacedName]; ok {
		if _, ok := s.NlbAllocationMap[val.NLB][val.Port]; ok {
			delete(s.NlbAllocationMap[val.NLB], val.Port)
		}
		delete(s.ServiceAllocationMap, serviceNamespacedName)
	}
}

func (s store) GetVacantNLBAndPortForService(_ context.Context, serviceNamespacedName string) (string, int, error) {
	for nlb, ports := range s.NlbAllocationMap {
		for port := 9000; port < 9050; port++ {
			if value, ok := ports[port]; !ok && value == nil {
				s.NlbAllocationMap[nlb][port] = &serviceNamespacedName
				return nlb, port, nil
			}
		}
	}
	return "", 0, errors.New("no vacancy found")
}

func New() Store {
	nlbData, nlbHostData := loadNlbData()
	return &store{
		ServiceAllocationMap: typeServiceAllocationMap{},
		NlbAllocationMap:     nlbData,
		NlbHosts:             nlbHostData,
	}
}

func loadNlbData() (typeNlbAllocationMap, map[string]string) {
	nlbData := typeNlbAllocationMap{}
	nlbHosts := map[string]string{}

	nlbCommaSeperatedList := os.Getenv("NLB_LIST")
	nlbList := strings.Split(nlbCommaSeperatedList, ",")
	if len(nlbList) == 0 {
		panic("env var NLB_LIST is empty. Needs comma seperated list as of key:value pair. No load balancers to manage.")
	}
	for _, nlbWithHost := range nlbList {
		nlb := strings.Split(nlbWithHost, ":")[0]
		nlbHost := strings.Split(nlbWithHost, ":")[1]
		if nlb != "" {
			nlbData[nlb] = map[int]*string{}
			nlbHosts[nlb] = nlbHost
		}

	}
	return nlbData, nlbHosts
}
