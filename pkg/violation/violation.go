package violation

import (
	"encoding/json"
	"fmt"
	"log"

	jsonpatch "github.com/evanphx/json-patch"
	controllerinternalinterfaces "github.com/nirmata/kube-policy/controller/internalinterfaces"
	kubeClient "github.com/nirmata/kube-policy/kubeclient"
	types "github.com/nirmata/kube-policy/pkg/apis/policy/v1alpha1"
	"github.com/nirmata/kube-policy/pkg/event/internalinterfaces"
	eventinternalinterfaces "github.com/nirmata/kube-policy/pkg/event/internalinterfaces"
	eventutils "github.com/nirmata/kube-policy/pkg/event/utils"
	violationinternalinterfaces "github.com/nirmata/kube-policy/pkg/violation/internalinterfaces"
	utils "github.com/nirmata/kube-policy/pkg/violation/utils"
	mergetypes "k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

type builder struct {
	kubeClient   *kubeClient.KubeClient
	controller   controllerinternalinterfaces.PolicyGetter
	eventBuilder eventinternalinterfaces.BuilderInternal
	logger       *log.Logger
}

type Builder interface {
	violationinternalinterfaces.ViolationGenerator
	ProcessViolation(info utils.ViolationInfo) error
	Patch(policy *types.Policy, updatedPolicy *types.Policy) error
	IsActive(kind string, resource string) (bool, error)
}

func NewViolationBuilder(
	kubeClient *kubeClient.KubeClient,
	eventBuilder internalinterfaces.BuilderInternal,
	logger *log.Logger) (Builder, error) {

	builder := &builder{}
	return builder, nil
}

func (b *builder) Create(info utils.ViolationInfo) error {
	err := b.ProcessViolation(info)
	if err != nil {
		return err
	}
	return nil
}

func (b *builder) SetController(controller controllerinternalinterfaces.PolicyGetter) {
	b.controller = controller
}

func (b *builder) ProcessViolation(info utils.ViolationInfo) error {
	// Get the policy
	policy, err := b.controller.GetPolicy(info.Policy)
	if err != nil {
		utilruntime.HandleError(err)
		return err
	}
	modifiedPolicy := policy.DeepCopy()
	modifiedViolations := []types.Violation{}

	for _, violation := range modifiedPolicy.PolicyViolation.Violations {
		ok, err := b.IsActive(info.Kind, info.Resource)
		if err != nil {
			utilruntime.HandleError(err)
			continue
		}
		if !ok {
			// Remove the violation
			// Create a removal event
			b.eventBuilder.AddEvent(eventutils.EventInfo{
				Kind:     "Policy",
				Resource: info.Resource,
				Rule:     info.Rule,
				Reason:   info.Reason,
				Message:  info.Message,
			})
			continue
		}
		// If violation already exists for this rule, we update the violation
		if violation.Kind == info.Kind &&
			violation.Resource == info.Resource &&
			violation.Rule == info.Rule {
			violation.Reason = info.Reason
			violation.Message = info.Message
		}
		modifiedViolations = append(modifiedViolations, violation)
	}
	modifiedPolicy.PolicyViolation.Violations = modifiedViolations
	return b.Patch(policy, modifiedPolicy)

}

func (b *builder) IsActive(kind string, resource string) (bool, error) {
	// Generate Merge Patch
	_, err := b.kubeClient.GetResource(kind, resource)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("unable to get resource %s ", resource))
		return false, err
	}
	return true, nil
}

// ProcessViolation(info utils.ViolationInfo) error
func (b *builder) Patch(policy *types.Policy, updatedPolicy *types.Policy) error {
	originalData, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	modifiedData, err := json.Marshal(updatedPolicy)
	if err != nil {
		return err
	}
	// generate merge patch
	patchBytes, err := jsonpatch.CreateMergePatch(originalData, modifiedData)
	if err != nil {
		return err
	}
	_, err = b.controller.PatchPolicy(policy.Name, mergetypes.MergePatchType, patchBytes)
	if err != nil {
		// Unable to patch
		return err
	}
	return nil
}
