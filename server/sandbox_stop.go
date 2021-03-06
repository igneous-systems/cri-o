package server

import (
	"fmt"

	"github.com/Sirupsen/logrus"
	"github.com/containers/storage"
	"github.com/docker/docker/pkg/mount"
	"github.com/docker/docker/pkg/symlink"
	"github.com/kubernetes-incubator/cri-o/oci"
	"github.com/opencontainers/selinux/go-selinux/label"
	"golang.org/x/net/context"
	"golang.org/x/sys/unix"
	pb "k8s.io/kubernetes/pkg/kubelet/api/v1alpha1/runtime"
)

// StopPodSandbox stops the sandbox. If there are any running containers in the
// sandbox, they should be force terminated.
func (s *Server) StopPodSandbox(ctx context.Context, req *pb.StopPodSandboxRequest) (*pb.StopPodSandboxResponse, error) {
	logrus.Debugf("StopPodSandboxRequest %+v", req)
	sb, err := s.getPodSandboxFromRequest(req.PodSandboxId)
	if err != nil {
		if err == errSandboxIDEmpty {
			return nil, err
		}

		// If the sandbox isn't found we just return an empty response to adhere
		// the the CRI interface which expects to not error out in not found
		// cases.

		resp := &pb.StopPodSandboxResponse{}
		logrus.Warnf("could not get sandbox %s, it's probably been stopped already: %v", req.PodSandboxId, err)
		return resp, nil
	}

	// Clean up sandbox networking and close its network namespace.
	hostNetwork := sb.NetNsPath() == ""
	s.networkStop(hostNetwork, sb)
	if err := sb.NetNsRemove(); err != nil {
		return nil, err
	}

	podInfraContainer := sb.infraContainer
	containers := sb.containers.List()
	containers = append(containers, podInfraContainer)

	for _, c := range containers {
		if err := s.Runtime().UpdateStatus(c); err != nil {
			return nil, err
		}
		cStatus := s.Runtime().ContainerStatus(c)
		if cStatus.Status != oci.ContainerStateStopped {
			if err := s.Runtime().StopContainer(c, -1); err != nil {
				return nil, fmt.Errorf("failed to stop container %s in pod sandbox %s: %v", c.Name(), sb.id, err)
			}
			if c.ID() == podInfraContainer.ID() {
				continue
			}
			if err := s.storageRuntimeServer.StopContainer(c.ID()); err != nil && err != storage.ErrContainerUnknown {
				// assume container already umounted
				logrus.Warnf("failed to stop container %s in pod sandbox %s: %v", c.Name(), sb.id, err)
			}
		}
		s.containerStateToDisk(c)
	}

	if err := label.ReleaseLabel(sb.processLabel); err != nil {
		return nil, err
	}

	// unmount the shm for the pod
	if sb.shmPath != "/dev/shm" {
		// we got namespaces in the form of
		// /var/run/containers/storage/overlay-containers/CID/userdata/shm
		// but /var/run on most system is symlinked to /run so we first resolve
		// the symlink and then try and see if it's mounted
		fp, err := symlink.FollowSymlinkInScope(sb.shmPath, "/")
		if err != nil {
			return nil, err
		}
		if mounted, err := mount.Mounted(fp); err == nil && mounted {
			if err := unix.Unmount(fp, unix.MNT_DETACH); err != nil {
				return nil, err
			}
		}
	}
	if err := s.storageRuntimeServer.StopContainer(sb.id); err != nil && err != storage.ErrContainerUnknown {
		logrus.Warnf("failed to stop sandbox container in pod sandbox %s: %v", sb.id, err)
	}

	resp := &pb.StopPodSandboxResponse{}
	logrus.Debugf("StopPodSandboxResponse: %+v", resp)
	return resp, nil
}

// StopAllPodSandboxes removes all pod sandboxes
func (s *Server) StopAllPodSandboxes() {
	logrus.Debugf("StopAllPodSandboxes")
	for _, sb := range s.state.sandboxes {
		pod := &pb.StopPodSandboxRequest{
			PodSandboxId: sb.id,
		}
		if _, err := s.StopPodSandbox(nil, pod); err != nil {
			logrus.Warnf("could not StopPodSandbox %s: %v", sb.id, err)
		}
	}
}
