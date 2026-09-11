package omitter

import "github.com/openshift/must-gather-clean/pkg/kube"

// MultiOmitter applies the configured omission rules without collecting a
// report. It is useful for preparatory passes that must follow the same
// inclusion policy as the final cleaning pass without reporting decisions
// twice.
type MultiOmitter struct {
	fileOmitters []FileOmitter
	k8sOmitters  []KubernetesResourceOmitter
}

func (m *MultiOmitter) OmitPath(path string) (bool, error) {
	for _, omitter := range m.fileOmitters {
		omit, err := omitter.OmitPath(path)
		if err != nil {
			return false, err
		}
		if omit {
			return true, nil
		}
	}
	return false, nil
}

func (m *MultiOmitter) OmitKubeResource(resourceList *kube.ResourceListWithPath) (bool, error) {
	for _, omitter := range m.k8sOmitters {
		omit, err := omitter.OmitKubeResource(resourceList)
		if err != nil {
			return false, err
		}
		if omit {
			return true, nil
		}
	}
	return false, nil
}

func NewMultiOmitter(fileOmitters []FileOmitter, k8sOmitters []KubernetesResourceOmitter) Omitter {
	return &MultiOmitter{
		fileOmitters: fileOmitters,
		k8sOmitters:  k8sOmitters,
	}
}
