package targetselector

import (
	"context"
	"github.com/loft-sh/devspace/pkg/util/stringutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/loft-sh/devspace/pkg/devspace/kubectl"
	"github.com/loft-sh/devspace/pkg/devspace/kubectl/selector"
	"github.com/loft-sh/devspace/pkg/util/log"
	v1 "k8s.io/api/core/v1"
)

// NewUntilNewestRunningWaitingStrategy creates a new waiting strategy
func NewUntilNewestRunningWaitingStrategy(initialDelay time.Duration, client kubectl.Client, namespace string) WaitingStrategy {
	return &untilNewestRunning{
		initialDelay: time.Now().Add(initialDelay),
		podInfoPrinter: &PodInfoPrinter{
			lastWarning: time.Now().Add(initialDelay),
			namespace:   namespace,
			client:      client,
		},
	}
}

// this waiting strategy will wait until the newest pod / container is up and running or fails
// it also waits initially for some time
type untilNewestRunning struct {
	initialDelay time.Time

	podInfoPrinter *PodInfoPrinter
}

func (u *untilNewestRunning) SelectPod(pods []*v1.Pod, log log.Logger) (bool, *v1.Pod, error) {
	now := time.Now()
	if now.Before(u.initialDelay) {
		return false, nil, nil
	} else if len(pods) == 0 {
		u.podInfoPrinter.PrintNotFoundWarning(log)
		return false, nil, nil
	}

	sort.Slice(pods, func(i, j int) bool {
		return selector.SortPodsByNewest(pods, i, j)
	})
	if HasPodProblem(pods[0]) {
		u.podInfoPrinter.PrintPodWarning(pods[0], log)
		return false, nil, nil
	} else if kubectl.GetPodStatus(pods[0]) != "Running" {
		u.podInfoPrinter.PrintPodInfo(pods[0], log)
		return false, nil, nil
	}

	return true, pods[0], nil
}

func (u *untilNewestRunning) SelectContainer(containers []*selector.SelectedPodContainer, log log.Logger) (bool, *selector.SelectedPodContainer, error) {
	now := time.Now()
	if now.Before(u.initialDelay) {
		return false, nil, nil
	} else if len(containers) == 0 {
		u.podInfoPrinter.PrintNotFoundWarning(log)
		return false, nil, nil
	}

	sort.Slice(containers, func(i, j int) bool {
		return selector.SortContainersByNewest(containers, i, j)
	})
	if HasPodProblem(containers[0].Pod) {
		u.podInfoPrinter.PrintPodWarning(containers[0].Pod, log)
		return false, nil, nil
	} else if !IsContainerRunning(containers[0]) {
		u.podInfoPrinter.PrintPodInfo(containers[0].Pod, log)
		return false, nil, nil
	}

	return true, containers[0], nil
}

type PodInfoPrinter struct {
	lastMutex   sync.Mutex
	lastWarning time.Time

	namespace   string
	client      kubectl.Client
	shownEvents []string
}

func (u *PodInfoPrinter) PrintPodInfo(pod *v1.Pod, log log.Logger) {
	u.lastMutex.Lock()
	defer u.lastMutex.Unlock()

	if time.Since(u.lastWarning) > time.Second*10 {
		status := kubectl.GetPodStatus(pod)
		log.Infof("DevSpace is waiting, because Pod %s has status: %s", pod.Name, status)

		u.shownEvents = displayWarnings(relevantObjectsFromPod(pod), pod.Namespace, u.client, u.shownEvents, log)
		u.lastWarning = time.Now()
	}
}

func (u *PodInfoPrinter) PrintNotFoundWarning(log log.Logger) {
	u.lastMutex.Lock()
	defer u.lastMutex.Unlock()

	if time.Since(u.lastWarning) > time.Second*10 {
		log.Warnf("DevSpace still couldn't find any Pods that match the selector. DevSpace will continue waiting, but this operation might timeout")

		u.shownEvents = displayWarnings([]relevantObject{
			{
				Kind: "StatefulSet",
			},
			{
				Kind: "Deployment",
			},
			{
				Kind: "ReplicaSet",
			},
		}, u.namespace, u.client, u.shownEvents, log)
		u.lastWarning = time.Now()
	}
}

func (u *PodInfoPrinter) PrintPodWarning(pod *v1.Pod, log log.Logger) {
	u.lastMutex.Lock()
	defer u.lastMutex.Unlock()

	if time.Since(u.lastWarning) > time.Second*10 {
		status := kubectl.GetPodStatus(pod)
		log.Warnf("Pod %s has critical status: %s. DevSpace will continue waiting, but this operation might timeout", pod.Name, status)
		u.lastWarning = time.Now()
	}
}

type relevantObject struct {
	Kind string
	Name string
	UID  string
}

func displayWarnings(relevantObjects []relevantObject, namespace string, client kubectl.Client, filter []string, log log.Logger) []string {
	events, err := client.KubeClient().CoreV1().Events(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.Debugf("Error retrieving pod events: %v", err)
		return nil
	}

	sort.Slice(events.Items, func(i, j int) bool {
		return events.Items[i].CreationTimestamp.Unix() > events.Items[j].CreationTimestamp.Unix()
	})
	for _, event := range events.Items {
		if event.Type != "Warning" {
			continue
		} else if stringutil.Contains(filter, event.Name) {
			continue
		} else if !eventMatches(&event, relevantObjects) {
			continue
		}

		log.Warnf("%s %s: %s (%s)", event.InvolvedObject.Kind, event.InvolvedObject.Name, event.Message, event.Reason)
		filter = append(filter, event.Name)
	}

	return filter
}

func relevantObjectsFromPod(pod *v1.Pod) []relevantObject {
	// search for persistent volume claims
	objects := []relevantObject{
		{
			Kind: "Pod",
			Name: pod.Name,
			UID:  string(pod.UID),
		},
	}
	for _, v := range pod.Spec.Volumes {
		if v.PersistentVolumeClaim != nil {
			objects = append(objects, relevantObject{
				Kind: "PersistentVolumeClaim",
				Name: v.PersistentVolumeClaim.ClaimName,
			})
		}

	}
	return objects
}

func eventMatches(event *v1.Event, objects []relevantObject) bool {
	for _, o := range objects {
		if o.Name != "" && event.InvolvedObject.Name != o.Name {
			continue
		} else if o.Kind != "" && event.InvolvedObject.Kind != o.Kind {
			continue
		} else if o.UID != "" && string(event.InvolvedObject.UID) != o.UID {
			continue
		}

		return true
	}

	return false
}

func IsContainerRunning(container *selector.SelectedPodContainer) bool {
	if container.Pod.DeletionTimestamp != nil {
		return false
	}
	for _, cs := range container.Pod.Status.InitContainerStatuses {
		if cs.Name == container.Container.Name && cs.State.Running != nil {
			return true
		}
	}
	for _, cs := range container.Pod.Status.ContainerStatuses {
		if cs.Name == container.Container.Name && cs.State.Running != nil {
			return true
		}
	}
	return false
}

func HasPodProblem(pod *v1.Pod) bool {
	status := kubectl.GetPodStatus(pod)
	status = strings.TrimPrefix(status, "Init:")
	return kubectl.CriticalStatus[status]
}
