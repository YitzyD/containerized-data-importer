package controller

import (
	"context"
	"strconv"
	// "fmt"
	// "reflect"

	"github.com/go-logr/logr"
	// "github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	// "k8s.io/apimachinery/pkg/api/meta"
	// metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	// "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	cdiv1 "kubevirt.io/containerized-data-importer/pkg/apis/core/v1beta1"
	// "kubevirt.io/containerized-data-importer/pkg/common"
)

const AnnCSICloneRequest = "k8s.io/CSICloneRequest"
const AnnCSICloneSource = "k8s.io/CSICloneSource"
const AnnCSICloneTarget = "k8s.io/CSICloneTarget"

type CSICloneReconciler struct {
	client   client.Client
	recorder record.EventRecorder
	scheme   *runtime.Scheme
	log      logr.Logger
}

func NewCSICloneController(mgr manager.Manager, log logr.Logger) (controller.Controller, error) {
	reconciler := &CSICloneReconciler{
		client:   mgr.GetClient(),
		scheme:   mgr.GetScheme(),
		log:      log.WithName("smartclone-controller"),
		recorder: mgr.GetEventRecorderFor("smartclone-controller"),
	}
	csiCloneController, err := controller.New("csiclone-controller", mgr, controller.Options{
		Reconciler: reconciler,
	})
	if err != nil {
		return nil, err
	}
	if err := addCSICloneControllerWatches(mgr, csiCloneController); err != nil {
		return nil, err
	}
	return csiCloneController, nil
}

func addCSICloneControllerWatches(mgr manager.Manager, csiCloneController controller.Controller) error {
	if err := cdiv1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}
	// Setup watches
	if err := csiCloneController.Watch(&source.Kind{Type: &corev1.PersistentVolumeClaim{}}, &handler.EnqueueRequestForOwner{
		OwnerType:    &cdiv1.DataVolume{},
		IsController: true,
	}, predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return shouldReconcileCSIClonePvc(e.Object.(*corev1.PersistentVolumeClaim))
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return shouldReconcileCSIClonePvc(e.ObjectNew.(*corev1.PersistentVolumeClaim))
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return shouldReconcileCSIClonePvc(e.Object.(*corev1.PersistentVolumeClaim))
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return shouldReconcileCSIClonePvc(e.Object.(*corev1.PersistentVolumeClaim))
		},
	}); err != nil {
		return err
	}
	return nil
}

func shouldReconcileCSIClonePvc(pvc *corev1.PersistentVolumeClaim) bool {
	if pvc.Status.Phase == corev1.ClaimLost {
		return false
	}

	val, ok := pvc.Annotations[AnnCSICloneRequest]
	return ok && val == "true"
}

// Reconcile the reconcile loop for smart cloning.
func (r *CSICloneReconciler) Reconcile(req reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithValues("Datavolume", req.NamespacedName)
	log.Info("reconciling CSI clone")
	pvc := &corev1.PersistentVolumeClaim{}
	if err := r.client.Get(context.TODO(), req.NamespacedName, pvc); err != nil {
		if k8serrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	isCloneSource := pvc.Annotations[AnnCSICloneSource]
	// isCloneTarget := pvc.Annotations[AnnCSICloneTarget]

	if s, err := strconv.ParseBool(isCloneSource); s && err == nil {
		return r.reconcileSourcePvc(log, pvc)
	}

	return reconcile.Result{}, nil
}

func (r *CSICloneReconciler) reconcileSourcePvc(log logr.Logger, pvc *corev1.PersistentVolumeClaim) (reconcile.Result, error) {
	if(pvc.Status.Phase == corev1.ClaimBound) {
		log.Info("PVC WAS BOUND")
		// Set PV of PVC to retain
		// Delete this PVC
		// Create target PVC with volume set to copied PV
		// Set PV to original policy
		return reconcile.Result{}, nil
	}
	return reconcile.Result{}, nil
}

// func (r *SmartCloneReconciler) updateSmartCloneStatusPhase(phase cdiv1.DataVolumePhase, dataVolume *cdiv1.DataVolume, newPVC *corev1.PersistentVolumeClaim) error {
// 	var dataVolumeCopy = dataVolume.DeepCopy()
// 	var event DataVolumeEvent

// 	switch phase {
// 	case cdiv1.SmartClonePVCInProgress:
// 		dataVolumeCopy.Status.Phase = cdiv1.SmartClonePVCInProgress
// 		event.eventType = corev1.EventTypeNormal
// 		event.reason = SmartClonePVCInProgress
// 		event.message = fmt.Sprintf(MessageSmartClonePVCInProgress, dataVolumeCopy.Spec.Source.PVC.Namespace, dataVolumeCopy.Spec.Source.PVC.Name)
// 		dataVolume.Status.Conditions = updateBoundCondition(dataVolume.Status.Conditions, newPVC)
// 		dataVolume.Status.Conditions = updateReadyCondition(dataVolume.Status.Conditions, corev1.ConditionFalse, "", "")
// 		dataVolume.Status.Conditions = updateCondition(dataVolume.Status.Conditions, cdiv1.DataVolumeRunning, corev1.ConditionTrue, MessageSmartClonePVCInProgress, SmartClonePVCInProgress)
// 	case cdiv1.Succeeded:
// 		dataVolumeCopy.Status.Phase = cdiv1.Succeeded
// 		event.eventType = corev1.EventTypeNormal
// 		event.reason = CloneSucceeded
// 		event.message = fmt.Sprintf(MessageCloneSucceeded, dataVolumeCopy.Spec.Source.PVC.Namespace, dataVolumeCopy.Spec.Source.PVC.Name, newPVC.Namespace, newPVC.Name)
// 		dataVolume.Status.Conditions = updateBoundCondition(dataVolume.Status.Conditions, newPVC)
// 		dataVolume.Status.Conditions = updateReadyCondition(dataVolume.Status.Conditions, corev1.ConditionTrue, "", "")
// 		dataVolume.Status.Conditions = updateCondition(dataVolume.Status.Conditions, cdiv1.DataVolumeRunning, corev1.ConditionFalse, cloneComplete, "Completed")
// 	}

// 	return r.emitEvent(dataVolume, dataVolumeCopy, &event, newPVC)
// }

// func (r *SmartCloneReconciler) emitEvent(dataVolume *cdiv1.DataVolume, dataVolumeCopy *cdiv1.DataVolume, event *DataVolumeEvent, newPVC *corev1.PersistentVolumeClaim) error {
// 	// Only update the object if something actually changed in the status.
// 	if !reflect.DeepEqual(dataVolume.Status, dataVolumeCopy.Status) {
// 		if err := r.client.Update(context.TODO(), dataVolumeCopy); err == nil {
// 			// Emit the event only when the status change happens, not every time
// 			if event.eventType != "" {
// 				r.recorder.Event(dataVolume, event.eventType, event.reason, event.message)
// 			}
// 		} else {
// 			return err
// 		}
// 	}
// 	return nil
// }

// func newPvcFromSnapshot(snapshot *snapshotv1.VolumeSnapshot, dataVolume *cdiv1.DataVolume) *corev1.PersistentVolumeClaim {
// 	labels := map[string]string{
// 		"cdi-controller":         snapshot.Name,
// 		common.CDILabelKey:       common.CDILabelValue,
// 		common.CDIComponentLabel: common.SmartClonerCDILabel,
// 	}
// 	ownerRef := metav1.GetControllerOf(snapshot)
// 	if ownerRef == nil {
// 		return nil
// 	}
// 	annotations := make(map[string]string)
// 	annotations[AnnSmartCloneRequest] = "true"
// 	annotations[AnnCloneOf] = "true"
// 	annotations[AnnRunningCondition] = string(corev1.ConditionFalse)
// 	annotations[AnnRunningConditionMessage] = cloneComplete
// 	annotations[AnnRunningConditionReason] = "Completed"

// 	return &corev1.PersistentVolumeClaim{
// 		ObjectMeta: metav1.ObjectMeta{
// 			Name:            snapshot.Name,
// 			Namespace:       snapshot.Namespace,
// 			Labels:          labels,https://github.com/coreweave/k8s-services/issues/106
// 			Annotations:     annotations,
// 			OwnerReferences: []metav1.OwnerReference{*ownerRef},
// 		},
// 		Spec: corev1.PersistentVolumeClaimSpec{
// 			DataSource: &corev1.TypedLocalObjectReference{
// 				Name:     snapshot.Name,
// 				Kind:     "VolumeSnapshot",
// 				APIGroup: &snapshotv1.SchemeGroupVersion.Group,
// 			},
// 			VolumeMode:       dataVolume.Spec.PVC.VolumeMode,
// 			AccessModes:      dataVolume.Spec.PVC.AccessModes,
// 			StorageClassName: dataVolume.Spec.PVC.StorageClassName,
// 			Resources: corev1.ResourceRequirements{
// 				Requests: dataVolume.Spec.PVC.Resources.Requests,
// 			},
// 		},
// 	}
// }
