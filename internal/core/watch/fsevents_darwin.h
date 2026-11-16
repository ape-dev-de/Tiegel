#include <stdint.h>

typedef struct tiegel_stream tiegel_stream;

tiegel_stream *tiegel_fsevents_start(uintptr_t handle, const char *path, double latency);
void tiegel_fsevents_stop(tiegel_stream *s);
