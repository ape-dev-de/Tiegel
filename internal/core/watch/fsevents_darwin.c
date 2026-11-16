//go:build darwin && cgo

#include <stdlib.h>
#include <CoreServices/CoreServices.h>
#include <dispatch/dispatch.h>
#include "_cgo_export.h"
#include "fsevents_darwin.h"

struct tiegel_stream {
	FSEventStreamRef ref;
	dispatch_queue_t queue;
};

static void tiegel_callback(ConstFSEventStreamRef ref, void *info, size_t n, void *paths,
	const FSEventStreamEventFlags flags[], const FSEventStreamEventId ids[]) {
	(void)ref;
	(void)ids;
	tiegelFSEvents((uintptr_t)info, n, (char **)paths, (uint32_t *)flags);
}

tiegel_stream *tiegel_fsevents_start(uintptr_t handle, const char *path, double latency) {
	CFStringRef p = CFStringCreateWithCString(NULL, path, kCFStringEncodingUTF8);
	if (p == NULL) {
		return NULL;
	}
	CFArrayRef paths = CFArrayCreate(NULL, (const void **)&p, 1, &kCFTypeArrayCallBacks);
	CFRelease(p);
	if (paths == NULL) {
		return NULL;
	}
	FSEventStreamContext ctx = {0, (void *)handle, NULL, NULL, NULL};
	FSEventStreamRef ref = FSEventStreamCreate(NULL, tiegel_callback, &ctx, paths,
		kFSEventStreamEventIdSinceNow, latency,
		kFSEventStreamCreateFlagFileEvents | kFSEventStreamCreateFlagNoDefer | kFSEventStreamCreateFlagWatchRoot);
	CFRelease(paths);
	if (ref == NULL) {
		return NULL;
	}
	dispatch_queue_t q = dispatch_queue_create("tiegel.watch", DISPATCH_QUEUE_SERIAL);
	FSEventStreamSetDispatchQueue(ref, q);
	if (!FSEventStreamStart(ref)) {
		FSEventStreamInvalidate(ref);
		FSEventStreamRelease(ref);
		dispatch_release(q);
		return NULL;
	}
	tiegel_stream *s = malloc(sizeof(tiegel_stream));
	if (s == NULL) {
		FSEventStreamStop(ref);
		FSEventStreamInvalidate(ref);
		FSEventStreamRelease(ref);
		dispatch_release(q);
		return NULL;
	}
	s->ref = ref;
	s->queue = q;
	return s;
}

static void tiegel_noop(void *ctx) { (void)ctx; }

void tiegel_fsevents_stop(tiegel_stream *s) {
	FSEventStreamStop(s->ref);
	FSEventStreamInvalidate(s->ref);
	// The queue is serial: once this no-op ran, no callback is running
	// or pending anymore.
	dispatch_sync_f(s->queue, NULL, tiegel_noop);
	FSEventStreamRelease(s->ref);
	dispatch_release(s->queue);
	free(s);
}
