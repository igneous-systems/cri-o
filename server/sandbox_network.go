package server

import (
	"fmt"
	"net"

	"github.com/Sirupsen/logrus"
	"k8s.io/kubernetes/pkg/kubelet/network/hostport"
)

// networkStart sets up the sandbox's network and returns the pod IP on success
// or an error
func (s *Server) networkStart(hostNetwork bool, sb *Sandbox) (string, error) {
	if hostNetwork {
		return s.bindAddress, nil
	}

	if err := s.netPlugin.SetUpPod(sb.NetNsPath(), sb.namespace, sb.kubeName, sb.id); err != nil {
		return "", fmt.Errorf("failed to create network for container %s in sandbox %s: %v", sb.name, sb.id, err)
	}

	var ip string
	if len(sb.portMappings) != 0 {
		ip, err := s.netPlugin.GetContainerNetworkStatus(sb.NetNsPath(), sb.namespace, sb.id, sb.name)
		if err != nil {
			return "", fmt.Errorf("failed to get network status for container %s in sandbox %s: %v", sb.name, sb.id, err)
		}

		ip4 := net.ParseIP(ip).To4()
		if ip4 == nil {
			return "", fmt.Errorf("failed to get valid ipv4 address for container %s in sandbox %s", sb.name, sb.id)
		}

		if err = s.hostportManager.Add(sb.id, &hostport.PodPortMapping{
			Name:         sb.name,
			PortMappings: sb.portMappings,
			IP:           ip4,
			HostNetwork:  false,
		}, "lo"); err != nil {
			return "", fmt.Errorf("failed to add hostport mapping for container %s in sandbox %s: %v", sb.name, sb.id, err)
		}

	}
	return ip, nil
}

// networkStop cleans up and removes a pod's network.  It is best-effort and
// must call the network plugin even if the network namespace is already gone
func (s *Server) networkStop(hostNetwork bool, sb *Sandbox) error {
	if !hostNetwork {
		if err2 := s.hostportManager.Remove(sb.id, &hostport.PodPortMapping{
			Name:         sb.name,
			PortMappings: sb.portMappings,
			HostNetwork:  false,
		}); err2 != nil {
			logrus.Warnf("failed to remove hostport for container %s in sandbox %s: %v",
				sb.name, sb.id, err2)
		}

		if err2 := s.netPlugin.TearDownPod(sb.NetNsPath(), sb.namespace, sb.kubeName, sb.id); err2 != nil {
			logrus.Warnf("failed to destroy network for container %s in sandbox %s: %v",
				sb.name, sb.id, err2)
		}
	}

	return nil
}
