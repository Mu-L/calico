// Copyright (c) 2017-2025 Tigera, Inc. All rights reserved.

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package clientv3

import (
	"context"
	"fmt"

	apiv3 "github.com/projectcalico/api/pkg/apis/projectcalico/v3"
	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/calico/libcalico-go/lib/names"
	"github.com/projectcalico/calico/libcalico-go/lib/options"
	validator "github.com/projectcalico/calico/libcalico-go/lib/validator/v3"
	"github.com/projectcalico/calico/libcalico-go/lib/watch"
)

// GlobalNetworkPolicyInterface has methods to work with GlobalNetworkPolicy resources.
type GlobalNetworkPolicyInterface interface {
	Create(ctx context.Context, res *apiv3.GlobalNetworkPolicy, opts options.SetOptions) (*apiv3.GlobalNetworkPolicy, error)
	Update(ctx context.Context, res *apiv3.GlobalNetworkPolicy, opts options.SetOptions) (*apiv3.GlobalNetworkPolicy, error)
	Delete(ctx context.Context, name string, opts options.DeleteOptions) (*apiv3.GlobalNetworkPolicy, error)
	Get(ctx context.Context, name string, opts options.GetOptions) (*apiv3.GlobalNetworkPolicy, error)
	List(ctx context.Context, opts options.ListOptions) (*apiv3.GlobalNetworkPolicyList, error)
	Watch(ctx context.Context, opts options.ListOptions) (watch.Interface, error)
}

// globalNetworkPolicies implements GlobalNetworkPolicyInterface
type globalNetworkPolicies struct {
	client client
}

// Create takes the representation of a GlobalNetworkPolicy and creates it.  Returns the stored
// representation of the GlobalNetworkPolicy, and an error, if there is any.
func (r globalNetworkPolicies) Create(ctx context.Context, res *apiv3.GlobalNetworkPolicy, opts options.SetOptions) (*apiv3.GlobalNetworkPolicy, error) {
	// Before creating the policy, check that the tier exists.
	tier := names.TierOrDefault(res.Spec.Tier)
	if _, err := r.client.resources.Get(ctx, options.GetOptions{}, apiv3.KindTier, noNamespace, tier); err != nil {
		log.WithError(err).Infof("Tier %v does not exist", tier)
		return nil, err
	}

	if res != nil {
		// Since we're about to default some fields, take a (shallow) copy of the input data
		// before we do so.
		resCopy := *res
		res = &resCopy
	}
	defaultPolicyTypesField(res.Spec.Ingress, res.Spec.Egress, &res.Spec.Types)

	if err := validator.Validate(res); err != nil {
		return nil, err
	}
	err := names.ValidateTieredPolicyName(res.Name, tier)
	if err != nil {
		return nil, err
	}

	// Add tier labels to policy for lookup.
	if tier != "default" {
		res.GetObjectMeta().SetLabels(addTierLabel(res.GetObjectMeta().GetLabels(), tier))
	}

	out, err := r.client.resources.Create(ctx, opts, apiv3.KindGlobalNetworkPolicy, res)
	if out != nil {
		// Add the tier labels if necessary
		out.GetObjectMeta().SetLabels(defaultTierLabelIfMissing(out.GetObjectMeta().GetLabels()))
		return out.(*apiv3.GlobalNetworkPolicy), err
	}

	// Add the tier labels if necessary
	res.GetObjectMeta().SetLabels(defaultTierLabelIfMissing(res.GetObjectMeta().GetLabels()))

	return nil, err
}

// Update takes the representation of a GlobalNetworkPolicy and updates it. Returns the stored
// representation of the GlobalNetworkPolicy, and an error, if there is any.
func (r globalNetworkPolicies) Update(ctx context.Context, res *apiv3.GlobalNetworkPolicy, opts options.SetOptions) (*apiv3.GlobalNetworkPolicy, error) {
	if res != nil {
		// Since we're about to default some fields, take a (shallow) copy of the input data
		// before we do so.
		resCopy := *res
		res = &resCopy
	}
	defaultPolicyTypesField(res.Spec.Ingress, res.Spec.Egress, &res.Spec.Types)

	if err := validator.Validate(res); err != nil {
		return nil, err
	}
	err := names.ValidateTieredPolicyName(res.Name, res.Spec.Tier)
	if err != nil {
		return nil, err
	}

	// Add tier labels to policy for lookup.
	tier := names.TierOrDefault(res.Spec.Tier)
	if tier != "default" {
		res.GetObjectMeta().SetLabels(addTierLabel(res.GetObjectMeta().GetLabels(), tier))
	}

	out, err := r.client.resources.Update(ctx, opts, apiv3.KindGlobalNetworkPolicy, res)
	if out != nil {
		// Add the tier labels if necessary
		out.GetObjectMeta().SetLabels(defaultTierLabelIfMissing(out.GetObjectMeta().GetLabels()))
		return out.(*apiv3.GlobalNetworkPolicy), err
	}

	// Add the tier labels if necessary
	res.GetObjectMeta().SetLabels(defaultTierLabelIfMissing(res.GetObjectMeta().GetLabels()))

	return nil, err
}

// Delete takes name of the GlobalNetworkPolicy and deletes it. Returns an error if one occurs.
func (r globalNetworkPolicies) Delete(ctx context.Context, name string, opts options.DeleteOptions) (*apiv3.GlobalNetworkPolicy, error) {
	out, err := r.client.resources.Delete(ctx, opts, apiv3.KindGlobalNetworkPolicy, noNamespace, name)
	if out != nil {
		// Add the tier labels if necessary
		out.GetObjectMeta().SetLabels(defaultTierLabelIfMissing(out.GetObjectMeta().GetLabels()))
		return out.(*apiv3.GlobalNetworkPolicy), err
	}
	return nil, err
}

// Get takes name of the GlobalNetworkPolicy, and returns the corresponding GlobalNetworkPolicy object,
// and an error if there is any.
func (r globalNetworkPolicies) Get(ctx context.Context, name string, opts options.GetOptions) (*apiv3.GlobalNetworkPolicy, error) {
	out, err := r.client.resources.Get(ctx, opts, apiv3.KindGlobalNetworkPolicy, noNamespace, name)
	if out != nil {
		// Add the tier labels if necessary
		out.GetObjectMeta().SetLabels(defaultTierLabelIfMissing(out.GetObjectMeta().GetLabels()))
		// Fill in the tier information from the policy name if we find it missing.
		// We expect backend policies to have the right name (prefixed with tier name).
		res_out := out.(*apiv3.GlobalNetworkPolicy)
		if res_out.Spec.Tier == "" {
			tier, tierErr := names.TierFromPolicyName(res_out.Name)
			if tierErr != nil {
				log.WithError(tierErr).Infof("Skipping setting tier for name %v", res_out.Name)
				return res_out, tierErr
			}
			res_out.Spec.Tier = tier
		}
		if res_out.Name != name {
			return nil, fmt.Errorf("resource not found GlobalNetworkPolicy(%s)", name)
		}
		return res_out, err
	}
	return nil, err
}

// List returns the list of GlobalNetworkPolicy objects that match the supplied options.
func (r globalNetworkPolicies) List(ctx context.Context, opts options.ListOptions) (*apiv3.GlobalNetworkPolicyList, error) {
	res := &apiv3.GlobalNetworkPolicyList{}
	// Add the name prefix if name is provided
	if opts.Name != "" && !opts.Prefix {
		opts.Name = names.TieredPolicyName(opts.Name)
	}

	if err := r.client.resources.List(ctx, opts, apiv3.KindGlobalNetworkPolicy, apiv3.KindGlobalNetworkPolicyList, res); err != nil {
		return nil, err
	}

	// Make sure the tier labels are added
	for i := range res.Items {
		res.Items[i].GetObjectMeta().SetLabels(defaultTierLabelIfMissing(res.Items[i].GetObjectMeta().GetLabels()))
		// Fill in the tier information from the policy name if we find it missing.
		// We expect backend policies to have the right name (prefixed with tier name).
		if res.Items[i].Spec.Tier == "" {
			tier, tierErr := names.TierFromPolicyName(res.Items[i].Name)
			if tierErr != nil {
				log.WithError(tierErr).Infof("Skipping setting tier for name %v", res.Items[i].Name)
				continue
			}
			res.Items[i].Spec.Tier = tier
		}
	}

	return res, nil
}

// Watch returns a watch.Interface that watches the globalNetworkPolicies that match the
// supplied options.
func (r globalNetworkPolicies) Watch(ctx context.Context, opts options.ListOptions) (watch.Interface, error) {
	// Add the name prefix if name is provided
	if opts.Name != "" {
		opts.Name = names.TieredPolicyName(opts.Name)
	}

	return r.client.resources.Watch(ctx, opts, apiv3.KindGlobalNetworkPolicy, &policyConverter{})
}

func defaultPolicyTypesField(ingressRules, egressRules []apiv3.Rule, types *[]apiv3.PolicyType) {
	if len(*types) == 0 {
		// Default the Types field according to what inbound and outbound rules are present
		// in the policy.
		if len(egressRules) == 0 {
			// Policy has no egress rules, so apply this policy to ingress only.  (Note:
			// intentionally including the case where the policy also has no ingress
			// rules.)
			*types = []apiv3.PolicyType{apiv3.PolicyTypeIngress}
		} else if len(ingressRules) == 0 {
			// Policy has egress rules but no ingress rules, so apply this policy to
			// egress only.
			*types = []apiv3.PolicyType{apiv3.PolicyTypeEgress}
		} else {
			// Policy has both ingress and egress rules, so apply this policy to both
			// ingress and egress.
			*types = []apiv3.PolicyType{apiv3.PolicyTypeIngress, apiv3.PolicyTypeEgress}
		}
	}
}

func addTierLabel(labels map[string]string, prefix string) map[string]string {
	// Create the map if it is nil
	if labels == nil {
		labels = make(map[string]string)
	}

	labels[apiv3.LabelTier] = prefix
	return labels
}

func defaultTierLabelIfMissing(labels map[string]string) map[string]string {
	// Create the map if it is nil
	if labels == nil {
		labels = make(map[string]string)
	}

	// Add the default labels if one is not set
	if _, ok := labels[apiv3.LabelTier]; !ok {
		labels[apiv3.LabelTier] = "default"
	}

	return labels
}

type policyConverter struct{}

func (pc *policyConverter) Convert(r resource) resource {
	r.GetObjectMeta().SetLabels(defaultTierLabelIfMissing(r.GetObjectMeta().GetLabels()))
	return r
}
