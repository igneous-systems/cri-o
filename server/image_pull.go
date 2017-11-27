package server

import (
	"encoding/base64"
	"strings"

	"github.com/Sirupsen/logrus"
	"github.com/containers/image/copy"
	"github.com/containers/image/types"
	"github.com/kubernetes-incubator/cri-o/pkg/storage"
	"golang.org/x/net/context"
	pb "k8s.io/kubernetes/pkg/kubelet/api/v1alpha1/runtime"
)

// PullImage pulls a image with authentication config.
func (s *Server) PullImage(ctx context.Context, req *pb.PullImageRequest) (*pb.PullImageResponse, error) {
	logrus.Debugf("PullImageRequest: %+v", req)
	// TODO(runcom?): deal with AuthConfig in req.GetAuth()
	// TODO: what else do we need here? (Signatures when the story isn't just pulling from docker://)
	image := ""
	img := req.GetImage()
	if img != nil {
		image = img.Image
	}

	var (
		username string
		password string
	)
	if req.GetAuth() != nil {
		username = req.GetAuth().Username
		password = req.GetAuth().Password
		if req.GetAuth().Auth != "" {
			var err error
			username, password, err = decodeDockerAuth(req.GetAuth().Auth)
			if err != nil {
				return nil, err
			}
		}
	}
	options := &copy.Options{
		SourceCtx: &types.SystemContext{},
	}
	// a not empty username should be sufficient to decide whether to send auth
	// or not I guess
	if username != "" {
		options.SourceCtx = &types.SystemContext{
			DockerAuthConfig: &types.DockerAuthConfig{
				Username: username,
				Password: password,
			},
		}
	}

	canPull, err := s.StorageImageServer().CanPull(image, options)
	if err != nil && !canPull {
		return nil, err
	}

	// let's be smart, docker doesn't repull if image already exists.
	var storedImage *storage.ImageResult
	storedImage, err = s.StorageImageServer().ImageStatus(s.ImageContext(), image)
	if err == nil {
		tmpImg, err := s.StorageImageServer().PrepareImage(s.ImageContext(), image, options)
		if err == nil {
			tmpImgConfigDigest := tmpImg.ConfigInfo().Digest
			if tmpImgConfigDigest.String() == "" {
				// this means we are playing with a schema1 image, in which
				// case, we're going to repull the image in any case
				logrus.Debugf("image config digest is empty, re-pulling image")
			} else if tmpImgConfigDigest.String() == storedImage.ConfigDigest.String() {
				logrus.Debugf("image %s already in store, skipping pull", img)
				resp := &pb.PullImageResponse{
					ImageRef: image,
				}
				logrus.Debugf("PullImageResponse: %+v", resp)
				return resp, nil
			}
		}
		logrus.Debugf("image in store has different ID, re-pulling %s", img)
	}

	if _, err := s.StorageImageServer().PullImage(s.ImageContext(), image, options); err != nil {
		return nil, err
	}
	resp := &pb.PullImageResponse{
		ImageRef: image,
	}
	logrus.Debugf("PullImageResponse: %+v", resp)
	return resp, nil
}

func decodeDockerAuth(s string) (string, string, error) {
	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", "", err
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		// if it's invalid just skip, as docker does
		return "", "", nil
	}
	user := parts[0]
	password := strings.Trim(parts[1], "\x00")
	return user, password, nil
}
