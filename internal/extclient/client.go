package extclient

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pgdoormanv1alpha1 "github.com/ermakov-oleg/cnpg-pg-doorman/api/v1alpha1"
)

// DefaultTTLSeconds is the default TTL in seconds of cache entries.
const DefaultTTLSeconds = 10

type cachedEntry struct {
	entry         client.Object
	fetchUnixTime int64
	ttl           time.Duration
}

func (e *cachedEntry) isExpired() bool {
	return time.Since(time.Unix(e.fetchUnixTime, 0)) > e.ttl
}

// ExtendedClient is a client that caches selected object types without relying on informers.
type ExtendedClient struct {
	client.Client
	cachedObjects []cachedEntry
	mux           *sync.Mutex
}

// NewExtendedClient returns a client capable of caching selected object types on Get.
func NewExtendedClient(baseClient client.Client) client.Client {
	return &ExtendedClient{
		Client: baseClient,
		mux:    &sync.Mutex{},
	}
}

func (e *ExtendedClient) isObjectCached(obj client.Object) bool {
	if _, ok := obj.(*corev1.Secret); ok {
		return true
	}
	if _, ok := obj.(*pgdoormanv1alpha1.PgDoorman); ok {
		return true
	}
	return false
}

// Get uses a cache for selected object types.
func (e *ExtendedClient) Get(
	ctx context.Context,
	key client.ObjectKey,
	obj client.Object,
	opts ...client.GetOption,
) error {
	if !e.isObjectCached(obj) {
		return e.Client.Get(ctx, key, obj, opts...)
	}
	return e.getCachedObject(ctx, key, obj, opts...)
}

func (e *ExtendedClient) getCachedObject(
	ctx context.Context,
	key client.ObjectKey,
	obj client.Object,
	opts ...client.GetOption,
) error {
	e.mux.Lock()
	defer e.mux.Unlock()

	// Evict all expired entries to prevent unbounded growth
	alive := e.cachedObjects[:0]
	for _, ce := range e.cachedObjects {
		if !ce.isExpired() {
			alive = append(alive, ce)
		}
	}
	e.cachedObjects = alive

	// Look for cache hit
	for _, cacheEntry := range e.cachedObjects {
		if cacheEntry.entry.GetNamespace() != key.Namespace || cacheEntry.entry.GetName() != key.Name {
			continue
		}
		if reflect.TypeOf(cacheEntry.entry) != reflect.TypeOf(obj) {
			continue
		}

		outVal := reflect.ValueOf(obj)
		objVal := reflect.ValueOf(cacheEntry.entry)
		if !objVal.Type().AssignableTo(outVal.Type()) {
			return fmt.Errorf("cache had type %s, but %s was asked for", objVal.Type(), outVal.Type())
		}
		reflect.Indirect(outVal).Set(reflect.Indirect(objVal))
		return nil
	}

	// Cache miss — fetch from API
	if err := e.Client.Get(ctx, key, obj, opts...); err != nil {
		return err
	}

	e.cachedObjects = append(e.cachedObjects, cachedEntry{
		entry:         obj.(runtime.Object).DeepCopyObject().(client.Object),
		fetchUnixTime: time.Now().Unix(),
		ttl:           time.Duration(DefaultTTLSeconds) * time.Second,
	})

	return nil
}

func (e *ExtendedClient) removeObject(object client.Object) {
	e.mux.Lock()
	defer e.mux.Unlock()

	for i, cache := range e.cachedObjects {
		if cache.entry.GetNamespace() == object.GetNamespace() &&
			cache.entry.GetName() == object.GetName() &&
			reflect.TypeOf(cache.entry) == reflect.TypeOf(object) {
			e.cachedObjects = append(e.cachedObjects[:i], e.cachedObjects[i+1:]...)
			return
		}
	}
}

// Update removes cached objects before updating.
func (e *ExtendedClient) Update(
	ctx context.Context,
	obj client.Object,
	opts ...client.UpdateOption,
) error {
	if e.isObjectCached(obj) {
		e.removeObject(obj)
	}
	return e.Client.Update(ctx, obj, opts...)
}

// Delete removes cached objects before deleting.
func (e *ExtendedClient) Delete(
	ctx context.Context,
	obj client.Object,
	opts ...client.DeleteOption,
) error {
	if e.isObjectCached(obj) {
		e.removeObject(obj)
	}
	return e.Client.Delete(ctx, obj, opts...)
}

// Patch removes cached objects before patching.
func (e *ExtendedClient) Patch(
	ctx context.Context,
	obj client.Object,
	patch client.Patch,
	opts ...client.PatchOption,
) error {
	if e.isObjectCached(obj) {
		e.removeObject(obj)
	}
	return e.Client.Patch(ctx, obj, patch, opts...)
}
