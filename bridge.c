#include "bridge.h"

static const cliproxy_host_api* stored_host = NULL;

void cliproxy_store_host(const cliproxy_host_api* host) {
    stored_host = host;
}

int cliproxy_call_host(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
    if (stored_host == NULL || stored_host->call == NULL) return 1;
    return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}

void cliproxy_free_host_buffer(void* ptr, size_t len) {
    if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) {
        stored_host->free_buffer(ptr, len);
    }
}
