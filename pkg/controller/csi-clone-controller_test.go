package controller

import (
	// "context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	// k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	log "sigs.k8s.io/controller-runtime/pkg/log"

	cdiv1 "kubevirt.io/containerized-data-importer/pkg/apis/core/v1beta1"
	// "kubevirt.io/containerized-data-importer/pkg/common"
)

var (
	csiCloneLog = log.Log.WithName("csi-clone-controller-test")
	mockStorageClassName = "mockStorageClass"
	mockStorageSize, _ = resource.ParseQuantity("1Gi")
	mockSourcePvc = corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mockSourcePVC",
			Namespace: "mockSourcePVCNamespace",
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
			Resources: corev1.ResourceRequirements {
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: mockStorageSize,
				},
			},
		},
	}
	mockDv = cdiv1.DataVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mockDv",
			Namespace: "mockDvNamespace",
		},
		Spec: cdiv1.DataVolumeSpec{
			Source: cdiv1.DataVolumeSource{
				PVC: &cdiv1.DataVolumeSourcePVC{
					Name: mockSourcePvc.Name,
					Namespace: mockSourcePvc.Namespace,
				},
			},
			PVC: &corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{
					corev1.ReadWriteOnce,
				},
				Resources: corev1.ResourceRequirements {
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: mockStorageSize,
					},
				},
				StorageClassName: &mockStorageClassName,
			},
		},
	}
	
	mockSourceClonePvc = NewVolumeClonePVC(&mockDv, *mockDv.Spec.PVC.StorageClassName, mockDv.Spec.PVC.AccessModes, CSICloneSourcePVC)
	// mockTargetClonePvc = NewVolumeClonePVC(&mockDv, *mockDv.Spec.PVC.StorageClassName, mockDv.Spec.PVC.AccessModes, CSICloneTargetPVC)
)

func createCSICloneReconciler(objects ...runtime.Object) *CSICloneReconciler {
	objs := []runtime.Object{}
	objs = append(objs, objects...)
	objs = append(objs, MakeEmptyCDICR())

	// Register operator types with the runtime scheme.
	s := scheme.Scheme
	_ = cdiv1.AddToScheme(s)
	_ = corev1.AddToScheme(s)

	rec := record.NewFakeRecorder(1)
	cl := fake.NewFakeClientWithScheme(s, objs...)

	return &CSICloneReconciler{
		client:   cl,
		scheme:   s,
		log:      csiCloneLog,
		recorder: rec,
	}
}

var _ = Describe("CSI-clone reconciliation", func() {
	DescribeTable("pvc reconciliation", 
		func(annotations map[string]string, phase corev1.PersistentVolumeClaimPhase, controllerKind string, expected bool) {
			isController := true
			pvc := corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: annotations,
					OwnerReferences: []metav1.OwnerReference{
						metav1.OwnerReference{
							Kind: controllerKind,
							Controller: &isController,
						},
					},
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: phase,
				},
			}
			Expect(shouldReconcileCSIClonePvc(&pvc)).To(Equal(expected))
		},
		Entry("should reconcile with AnnCSICloneRequest = true, claim bound, and controller kind = DataVolume", map[string]string{AnnCSICloneRequest: "true"}, corev1.ClaimBound, "DataVolume", true),
		Entry("should not reconcile with AnnCSICloneRequest unset", map[string]string{}, corev1.ClaimBound, "DataVolume", false),
		Entry("should not reconcile with claim lost", map[string]string{AnnCSICloneRequest: "true"}, corev1.ClaimLost, "DataVolume", false),
		Entry("should not reconcile with incorrect controller reference", map[string]string{AnnCSICloneRequest: "true"}, corev1.ClaimLost, "NotADataVolume", false),
	)
})

var _ = Describe("CSI-clone reconcile loop", func() {
	var (
		reconciler *CSICloneReconciler
	)
	AfterEach(func() {
		if reconciler != nil {
			close(reconciler.recorder.(*record.FakeRecorder).Events)
			reconciler = nil
		}
	})

	It("should return nil if the pvc is not found", func() {
		reconciler := createCSICloneReconciler()
		_, err := reconciler.Reconcile(reconcile.Request{NamespacedName: types.NamespacedName{Name: "no-pvc", Namespace: "default" }})
		Expect(err).ToNot(HaveOccurred())
	})
	It("should return nil if the pvc is neither annotated AnnCSICloneSource or AnnCSICloneTarget", func() {
		reconciler := createCSICloneReconciler(mockSourcePvc.DeepCopy())
		_, err := reconciler.Reconcile(reconcile.Request{NamespacedName: types.NamespacedName{Name: mockSourcePvc.Name, Namespace: mockSourcePvc.Namespace }})
		Expect(err).ToNot(HaveOccurred())
	})

	Context("when a AnnCSICloneSource annotated PVC is reconciled", func() {
		It("should return an error if the datavolume is not found", func() {
			reconciler := createCSICloneReconciler(mockSourceClonePvc.DeepCopy())
			_, err := reconciler.Reconcile(reconcile.Request{NamespacedName: types.NamespacedName{Name: mockSourceClonePvc.Name, Namespace: mockSourceClonePvc.Namespace}})
			Expect(err).To(HaveOccurred())
		})
	})
})
