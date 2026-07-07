package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type DataProcessingUnitSpec struct {
	DpuProductName string `json:"dpuProductName,omitempty"`
	NodeName       string `json:"nodeName,omitempty"`
}

type DataProcessingUnitStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

func (in *DataProcessingUnitStatus) DeepCopyInto(out *DataProcessingUnitStatus) {
	*out = *in
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]metav1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

type DataProcessingUnit struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DataProcessingUnitSpec   `json:"spec,omitempty"`
	Status            DataProcessingUnitStatus `json:"status,omitempty"`
}

type DataProcessingUnitList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DataProcessingUnit `json:"items"`
}

func (in *DataProcessingUnit) DeepCopyInto(out *DataProcessingUnit) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = in.Spec
	in.Status.DeepCopyInto(&out.Status)
}

func (in *DataProcessingUnit) DeepCopy() *DataProcessingUnit {
	if in == nil { return nil }
	out := new(DataProcessingUnit)
	in.DeepCopyInto(out)
	return out
}

func (in *DataProcessingUnit) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil { return c }
	return nil
}

func (in *DataProcessingUnitList) DeepCopyInto(out *DataProcessingUnitList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]DataProcessingUnit, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

func (in *DataProcessingUnitList) DeepCopy() *DataProcessingUnitList {
	if in == nil { return nil }
	out := new(DataProcessingUnitList)
	in.DeepCopyInto(out)
	return out
}

func (in *DataProcessingUnitList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil { return c }
	return nil
}
