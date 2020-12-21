package controller

import (
	"context"
	"reflect"
	"strconv"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ref "k8s.io/client-go/tools/reference"

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
)

const (
	AnnCSICloneRequest     = "cdi.kubevirt.io/CSICloneRequest"
	AnnCSICloneDVNamespace = "cdi.kubevirt.io/CSICloneDVNamespace"
	AnnCSICloneSource      = "cdi.kubevirt.io/CSICloneSource"
	AnnCSICloneTarget      = "cdi.kubevirt.io/CSICloneTarget"
	AnnCSICloneCapable     = "cdi.kubevirt.io/CSICloneVolumeCapable"
)

type CSIClonePVCType string

const (
	CSICloneSourcePVC CSIClonePVCType = AnnCSICloneSource
	CSICloneTargetPVC CSIClonePVCType = AnnCSICloneTarget
)

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
		log:      log.WithName("csiclone-controller"),
		recorder: mgr.GetEventRecorderFor("csiclone-controller"),
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

	if err := csiCloneController.Watch(&source.Kind{Type: &corev1.PersistentVolumeClaim{}}, &handler.EnqueueRequestForObject{}, predicate.Funcs{
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

// Reconcile the reconcile loop for csi cloning.
func (r *CSICloneReconciler) Reconcile(req reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithValues("PVC", req.NamespacedName)
	log.Info("reconciling csi clone")
	pvc := &corev1.PersistentVolumeClaim{}
	if err := r.client.Get(context.TODO(), req.NamespacedName, pvc); err != nil {
		if k8serrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	isCloneSource := pvc.Annotations[AnnCSICloneSource]
	isCloneTarget := pvc.Annotations[AnnCSICloneTarget]

	if s, err := strconv.ParseBool(isCloneSource); s && err == nil {
		return r.reconcileSourcePvc(log, pvc)
	}

	if s, err := strconv.ParseBool(isCloneTarget); s && err == nil {
		return r.reconcileTargetPVC(log, pvc)
	}

	return reconcile.Result{}, nil
}

func verifyTargetPVC(targetPvc *corev1.PersistentVolumeClaim) error {
	if(targetPvc.Status.Phase == corev1.ClaimLost) {
		return fmt.Errorf("Target clone pvc claim lost")
	}	else if(targetPvc.Status.Phase == corev1.ClaimPending) {
		controllingDv := metav1.GetControllerOf(targetPvc)
		if(controllingDv != nil && controllingDv.Kind != "DataVolume") {
			return fmt.Errorf("Invalid controller for target clone pvc")
		} else {
			return nil
		}
	}
	return nil
}

func (r *CSICloneReconciler) reconcileSourcePvc(log logr.Logger, pvc *corev1.PersistentVolumeClaim) (reconcile.Result, error) {
	// Get DataVolume of PVC
	dv := &cdiv1.DataVolume{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Name: metav1.GetControllerOf(pvc).Name, Namespace: pvc.Annotations[AnnCSICloneDVNamespace]}, dv); err != nil {
		return reconcile.Result{}, err
	}

	if pvc.Status.Phase == corev1.ClaimBound {
		pv := &corev1.PersistentVolume{}
		if err := r.client.Get(context.TODO(), types.NamespacedName{Name: pvc.Spec.VolumeName}, pv); err != nil {
			if k8serrors.IsNotFound(err) {
				return reconcile.Result{}, nil
			}
			return reconcile.Result{}, err
		}
		// Deep copy pv object for mutation
		pvCopy := pv.DeepCopy()

		dv := &cdiv1.DataVolume{}
		if err := r.client.Get(context.TODO(), types.NamespacedName{Name: metav1.GetControllerOf(pvc).Name, Namespace: pvc.Annotations[AnnCSICloneDVNamespace]}, dv); err != nil {
			return reconcile.Result{}, err
		}

		targetClonerPvc := NewVolumeClonePVC(dv, *pvc.Spec.StorageClassName, pvc.Spec.AccessModes, CSICloneTargetPVC)

		targetClonerPvc.Spec.VolumeName = pv.Name

		if err := r.client.Create(context.TODO(), targetClonerPvc); err != nil {
			if k8serrors.IsAlreadyExists(err) {
				if(verifyTargetPVC(targetClonerPvc) == nil) {
					// Target clone pvc already exists, and is valid; delete Source clone PVC
					return reconcile.Result{}, r.client.Delete(context.TODO(), pvc)
			}
			}
			return reconcile.Result{}, err
		}

		claimRef, err := ref.GetReference(r.scheme, targetClonerPvc)
		if err != nil {
			return reconcile.Result{}, err
		}

		pv.Spec.ClaimRef = claimRef
		if err := r.client.Update(context.TODO(), pv); err != nil {
			return reconcile.Result{}, err
		}

		if err := r.client.Delete(context.TODO(), pvc); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	} else if pvc.Status.Phase == corev1.ClaimLost {
		// If Source pvc claim is lost
		// Verify if target clone pvc valid
		targetClonerPvc := NewVolumeClonePVC(dv, *pvc.Spec.StorageClassName, pvc.Spec.AccessModes, CSICloneTargetPVC)
		targetPvc := &corev1.PersistentVolumeClaim{}
		if err := r.client.Get(context.TODO(), types.NamespacedName{Name: targetClonerPvc.Name, Namespace: targetClonerPvc.Namespace}, targetPvc); err != nil {
			if(k8serrors.IsNotFound(err)) {
				// Target clone pvc was either not created, or delete during cloning process
				// Set dv err status CloneSourcePVLost
				return reconcile.Result{}, r.updateDVStatus(cdiv1.CloneSourcePVLost, dv)
			}
			// Unable to get Target clone pvc for other reason; Requeue with error
			return reconcile.Result{}, err
		}
		if(verifyTargetPVC(targetPvc) != nil) {
			// Target clone pvc is not valid
			// Set dv err status CloneSourcePVLost
			return reconcile.Result{}, r.updateDVStatus(cdiv1.CloneSourcePVLost, dv)
		} else {
			// Target clone pvc successfully created, delete Source clone pvc
			if err := r.client.Delete(context.TODO(), pvc); err != nil {
				return reconcile.Result{}, err
			}
		}
	}
	return reconcile.Result{}, nil
}

func (r *CSICloneReconciler) reconcileTargetPVC(log logr.Logger, pvc *corev1.PersistentVolumeClaim) (reconcile.Result, error) {
	if pvc.Status.Phase == corev1.ClaimBound {
		dv := &cdiv1.DataVolume{}
		if err := r.client.Get(context.TODO(), types.NamespacedName{Name: metav1.GetControllerOf(pvc).Name, Namespace: pvc.Namespace}, dv); err != nil {
			if k8serrors.IsNotFound(err) {
				// Datavolume deleted
				return reconcile.Result{}, nil
			}
			return reconcile.Result{}, err
		}

		if err := r.updateDVStatus(cdiv1.PVCBound, dv); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}
	return reconcile.Result{}, nil
}

func (r *CSICloneReconciler) updateDVStatus(phase cdiv1.DataVolumePhase, dataVolume *cdiv1.DataVolume) error {
	var dataVolumeCopy = dataVolume.DeepCopy()
	var event DataVolumeEvent

	switch phase {
	case cdiv1.CloneSourcePVLost:
		dataVolumeCopy.Status.Phase = cdiv1.CloneSourcePVLost
		event.eventType = corev1.EventTypeWarning
		event.reason = string(cdiv1.CloneSourcePVLost)
		event.message = "Source PVC lost its PV binding during the cloning process."
	case cdiv1.CloneTargetPVCLost:
		dataVolumeCopy.Status.Phase = cdiv1.CloneTargetPVCLost
		event.eventType = corev1.EventTypeWarning
		event.reason = string(cdiv1.CloneTargetPVCLost)
		event.message = fmt.Sprintf("Target PVC %s lost during the cloning process.", dataVolume.Name)
	case cdiv1.PVCBound:
		dataVolumeCopy.Status.Phase = cdiv1.PVCBound
	}

	return r.emitEvent(dataVolume, dataVolumeCopy, &event)
}

func (r *CSICloneReconciler) emitEvent(dataVolume *cdiv1.DataVolume, dataVolumeCopy *cdiv1.DataVolume, event *DataVolumeEvent) error {
	// Only update the object if something actually changed in the status.
	if !reflect.DeepEqual(dataVolume.Status, dataVolumeCopy.Status) {
		if err := r.client.Update(context.TODO(), dataVolumeCopy); err == nil {
			// Emit the event only when the status change happens, not every time
			if event.eventType != "" {
				r.recorder.Event(dataVolume, event.eventType, event.reason, event.message)
			}
		} else {
			return err
		}
	}
	return nil
}
