// Copyright 2022 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package kind

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"time"

	ecc "github.com/hacbs-contract/enterprise-contract-controller/api/v1alpha1"
	"github.com/tektoncd/cli/pkg/formatted"
	tknv1beta1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	tekton "github.com/tektoncd/pipeline/pkg/client/clientset/versioned/typed/pipeline/v1beta1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/hacbs-contract/ec-cli/acceptance/kubernetes/types"
	"github.com/hacbs-contract/ec-cli/acceptance/kustomize"
	"github.com/hacbs-contract/ec-cli/acceptance/testenv"
)

// createPolicyObject creates the EnterpriseContractPolicy object with the given
// Spec from the specification expected as a JSON string
func (k *kindCluster) createPolicyObject(ctx context.Context, specification string) (*ecc.EnterpriseContractPolicy, error) {
	t := testenv.FetchState[testState](ctx)

	policySpec := ecc.EnterpriseContractPolicySpec{}
	if err := json.Unmarshal([]byte(specification), &policySpec); err != nil {
		return nil, err
	}

	return &ecc.EnterpriseContractPolicy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: ecc.GroupVersion.String(),
			Kind:       "EnterpriseContractPolicy",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: t.namespace,
		},
		Spec: policySpec,
	}, nil

}

// createPolicy creates the EnterpriseContractPolicy custom resource in the test
// context namespace
func (k *kindCluster) createPolicy(ctx context.Context, policy *ecc.EnterpriseContractPolicy) error {
	policyMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&policy)
	if err != nil {
		return err
	}

	unstructuredPolicy := unstructured.Unstructured{}
	unstructuredPolicy.SetUnstructuredContent(policyMap)

	t := testenv.FetchState[testState](ctx)
	created, err := k.dynamic.Resource(ecc.GroupVersion.WithResource("enterprisecontractpolicies")).Namespace(t.namespace).Create(ctx, &unstructuredPolicy, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	// remember the created policy in the test context
	t.policy = created.GetName()

	return nil
}

// CreateNamedPolicy creates a EnterpriseContractPolicy custom resource with the
// given name and specification in the test context namespace
func (k *kindCluster) CreateNamedPolicy(ctx context.Context, name string, specification string) error {
	policy, err := k.createPolicyObject(ctx, specification)
	if err != nil {
		return err
	}

	policy.ObjectMeta.Name = name

	return k.createPolicy(ctx, policy)
}

// CreatePolicy creates an EnterpriseContractPolicy with a random name and the
// provided specification in the test context namespace
func (k *kindCluster) CreatePolicy(ctx context.Context, specification string) error {
	policy, err := k.createPolicyObject(ctx, specification)
	if err != nil {
		return err
	}
	policy.ObjectMeta.GenerateName = "acceptance-policy-"

	return k.createPolicy(ctx, policy)
}

// CreateNamespace creates a randomly-named namespace for the test to execute in
// and stores it in the test context
func (k *kindCluster) CreateNamespace(ctx context.Context) (context.Context, error) {
	t := &testState{}
	ctx, err := testenv.SetupState(ctx, &t)
	if err != nil {
		return ctx, err
	}

	if t.namespace != "" {
		// already created
		return ctx, nil
	}

	namespace, err := k.client.CoreV1().Namespaces().Create(ctx, &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "acceptance-",
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return ctx, err
	}

	t.namespace = namespace.GetName()

	// to prevent concurrency issues we lock/unlock the environment mutex here
	render := func() ([]byte, error) {
		envMutex.Lock()
		if err := os.Setenv("WORK_NAMESPACE", fmt.Sprint(t.namespace)); err != nil {
			return nil, err
		}

		defer func() {
			_ = os.Unsetenv("WORK_NAMESPACE") // ignore errors
			envMutex.Unlock()
		}()

		return kustomize.Render(path.Join("work"))
	}

	yaml, err := render()
	if err != nil {
		return ctx, err
	}

	return ctx, applyConfiguration(ctx, k, yaml)
}

// stringParam generates a Tekton Parameter optionally expanding the
// `${NAMESPACE}` and `${POLICY_NAME}` variables
func stringParam(name, value string, t *testState) tknv1beta1.Param {
	v := os.Expand(value, func(variable string) string {
		switch variable {
		case "NAMESPACE":
			return t.namespace
		case "POLICY_NAME":
			return t.policy
		}

		return ""
	})

	return tknv1beta1.Param{
		Name: name,
		Value: tknv1beta1.ParamValue{
			Type:      tknv1beta1.ParamTypeString,
			StringVal: v,
		},
	}
}

// RunTask creates a TaskRun with a random name running the Task from the Tekton
// Bundle of a specific version with the provided parameters
func (k *kindCluster) RunTask(ctx context.Context, version string, params map[string]string) error {
	t := testenv.FetchState[testState](ctx)

	tkn, err := tekton.NewForConfig(k.config)
	if err != nil {
		return err
	}

	tknParams := make([]tknv1beta1.Param, 0, len(params))
	for n, v := range params {
		tknParams = append(tknParams, stringParam(n, v, t))
	}

	timeout, err := time.ParseDuration("10m")
	if err != nil {
		return err
	}

	tr, err := tkn.TaskRuns(t.namespace).Create(ctx, &tknv1beta1.TaskRun{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "acceptance-taskrun-",
		},
		Spec: tknv1beta1.TaskRunSpec{
			Params:             tknParams,
			ServiceAccountName: "default",
			TaskRef: &tknv1beta1.TaskRef{
				ResolverRef: tknv1beta1.ResolverRef{
					Resolver: "bundles",
					Params: []tknv1beta1.Param{
						stringParam("bundle", fmt.Sprintf("registry.image-registry.svc.cluster.local:%d/ec-task-bundle:%s", k.registryPort, version), t),
						stringParam("name", "verify-enterprise-contract", t),
						stringParam("kind", "task", t),
					},
				},
			},
			Timeout: &metav1.Duration{
				Duration: timeout,
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	// store the TaskRun name in the test context
	t.taskRun = tr.GetName()

	return nil
}

// AwaitUntilTaskIsDone waits for the TaskRun whose name is stored in the test
// context is done and reports if it was successful
func (k *kindCluster) AwaitUntilTaskIsDone(ctx context.Context) (bool, error) {
	t := testenv.FetchState[testState](ctx)

	tkn, err := tekton.NewForConfig(k.config)
	if err != nil {
		return false, err
	}

	watch, err := tkn.TaskRuns(t.namespace).Watch(ctx, metav1.SingleObject(metav1.ObjectMeta{
		Name: t.taskRun,
	}))
	if err != nil {
		return false, err
	}

	for e := range watch.ResultChan() {
		tr, ok := e.Object.(*tknv1beta1.TaskRun)
		if !ok {
			return false, fmt.Errorf("received unexpected object when watching the TaskRun: %v", e.Object.GetObjectKind())
		}

		if tr.IsDone() {
			return tr.IsSuccessful(), nil
		}
	}

	return false, nil
}

// TaskInfo provides information on the TaskRun, invoked after the TaskRun is
// done for most complete information
func (k *kindCluster) TaskInfo(ctx context.Context) (*types.TaskInfo, error) {
	t := testenv.FetchState[testState](ctx)

	tkn, err := tekton.NewForConfig(k.config)
	if err != nil {
		return nil, err
	}

	tr, err := tkn.TaskRuns(t.namespace).Get(ctx, t.taskRun, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	info := types.TaskInfo{
		Namespace: t.namespace,
		Name:      t.taskRun,
		Status:    formatted.Condition(tr.Status.Conditions),
	}

	info.Params = map[string]any{}
	for _, p := range tr.Spec.Params {
		info.Params[p.Name] = paramValue(p.Value)
	}

	info.Steps = make([]types.Step, 0, len(tr.Status.Steps))
	for _, s := range tr.Status.Steps {
		logs, err := k.logs(ctx, t.namespace, tr.Status.PodName, s.ContainerName)
		if err != nil {
			return nil, err
		}

		info.Steps = append(info.Steps, types.Step{
			Name:   s.Name,
			Status: s.Terminated.Reason,
			Logs:   logs,
		})
	}

	return &info, nil
}

func paramValue(v tknv1beta1.ParamValue) any {
	switch v.Type {
	case tknv1beta1.ParamTypeString:
		return v.StringVal
	case tknv1beta1.ParamTypeArray:
		return v.ArrayVal
	case tknv1beta1.ParamTypeObject:
		return v.ObjectVal
	}

	return "<unset>"
}

func (k *kindCluster) logs(ctx context.Context, namespace, pod, container string) (string, error) {
	stream, err := k.client.CoreV1().Pods(namespace).GetLogs(pod, &v1.PodLogOptions{Container: container}).Stream(ctx)
	if err != nil {
		return "", err
	}
	defer stream.Close()

	bytes, err := io.ReadAll(stream)
	if err != nil {
		return "", err
	}

	return string(bytes), nil
}
