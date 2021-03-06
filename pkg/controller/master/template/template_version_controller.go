package template

import (
	"reflect"
	"sort"
	"sync"

	apisv1alpha1 "github.com/rancher/harvester/pkg/apis/harvester.cattle.io/v1alpha1"
	ctlapisv1alpha1 "github.com/rancher/harvester/pkg/generated/controllers/harvester.cattle.io/v1alpha1"
	"github.com/rancher/harvester/pkg/ref"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	TemplateLabel = "template.harvester.cattle.io/templateID"
)

// templateVersionHandler sets metadata and status to templateVersion objects,
// including labels, ownerReference and status.Version.
type templateVersionHandler struct {
	templateCache      ctlapisv1alpha1.VirtualMachineTemplateCache
	templateVersions   ctlapisv1alpha1.VirtualMachineTemplateVersionClient
	templateController ctlapisv1alpha1.VirtualMachineTemplateController
	mu                 sync.RWMutex //use mutex to avoid create duplicated version
}

func (h *templateVersionHandler) OnChanged(key string, tv *apisv1alpha1.VirtualMachineTemplateVersion) (*apisv1alpha1.VirtualMachineTemplateVersion, error) {
	if tv == nil || tv.DeletionTimestamp != nil {
		return nil, nil
	}

	ns, templateName := ref.Parse(tv.Spec.TemplateID)
	template, err := h.templateCache.Get(ns, templateName)
	if err != nil {
		return nil, err
	}

	copyObj := tv.DeepCopy()

	//set labels
	if copyObj.Labels == nil {
		copyObj.Labels = make(map[string]string)
	}
	if _, ok := copyObj.Labels[TemplateLabel]; !ok {
		copyObj.Labels[TemplateLabel] = templateName
	}

	//set ownerReference
	flagTrue := true
	ownerRef := []metav1.OwnerReference{{
		Name:               template.Name,
		APIVersion:         template.APIVersion,
		UID:                template.UID,
		Kind:               template.Kind,
		BlockOwnerDeletion: &flagTrue,
		Controller:         &flagTrue,
	}}

	if len(copyObj.OwnerReferences) == 0 {
		copyObj.OwnerReferences = ownerRef
	} else if !isVersionOwnedByTemplate(copyObj, template) {
		copyObj.OwnerReferences = append(copyObj.OwnerReferences, ownerRef...)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	//set version
	if !apisv1alpha1.VersionAssigned.IsTrue(copyObj) {
		existLatestVersion, _, err := getTemplateLatestVersion(tv.Namespace, tv.Spec.TemplateID, h.templateVersions)
		if err != nil {
			return nil, err
		}

		latestVersion := existLatestVersion + 1
		copyObj.Status.Version = latestVersion
		apisv1alpha1.VersionAssigned.True(copyObj)
	}

	if !reflect.DeepEqual(copyObj, tv) {
		if _, err = h.templateVersions.Update(copyObj); err != nil {
			return copyObj, err
		}
		h.templateController.Enqueue(ns, templateName)
	}

	return copyObj, nil
}

func getTemplateLatestVersion(templateVersionNs, templateID string, templateVersions ctlapisv1alpha1.VirtualMachineTemplateVersionClient) (int, *apisv1alpha1.VirtualMachineTemplateVersion, error) {
	var latestVersion int
	list, err := templateVersions.List(templateVersionNs, metav1.ListOptions{})
	if err != nil {
		return latestVersion, nil, err
	}

	var tvs []apisv1alpha1.VirtualMachineTemplateVersion
	for _, v := range list.Items {
		if v.Spec.TemplateID == templateID {
			tvs = append(tvs, v)
		}
	}

	if len(tvs) == 0 {
		return 0, nil, nil
	}

	sort.Sort(templateVersionByCreationTimestamp(tvs))
	for _, v := range tvs {
		if apisv1alpha1.VersionAssigned.IsTrue(v) {
			return v.Status.Version, &v, nil
		}
	}

	return 0, nil, nil
}

// templateVersionByCreationTimestamp sorts a list of TemplateVersion by creation timestamp, using their names as a tie breaker.
type templateVersionByCreationTimestamp []apisv1alpha1.VirtualMachineTemplateVersion

func (o templateVersionByCreationTimestamp) Len() int      { return len(o) }
func (o templateVersionByCreationTimestamp) Swap(i, j int) { o[i], o[j] = o[j], o[i] }
func (o templateVersionByCreationTimestamp) Less(i, j int) bool {
	if o[i].CreationTimestamp.Equal(&o[j].CreationTimestamp) {
		return o[i].Name < o[j].Name
	}
	return o[j].CreationTimestamp.Before(&o[i].CreationTimestamp)
}

func isVersionOwnedByTemplate(version *apisv1alpha1.VirtualMachineTemplateVersion, template *apisv1alpha1.VirtualMachineTemplate) bool {
	for _, v := range version.OwnerReferences {
		if v.UID == template.UID {
			return true
		}
	}
	return false
}
