#ifndef GPU_PROVER_H
#define GPU_PROVER_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C"
{
#endif

#if defined(_WIN32)
#define EXPORT __declspec(dllexport)
#else
#define EXPORT __attribute__((visibility("default")))
#endif

    // Allocates and fills VRAM buffer (e.g. 2GB) with deterministic pseudorandom state
    EXPORT int init_gpu_memory(int device_id, size_t size_bytes);

    // Executes memory-bandwidth bound traversal across VRAM
    EXPORT int run_gpu_memory_challenge(
        int device_id,
        const uint8_t *challenge_data,
        size_t challenge_len,
        uint8_t *out_digest_32);

    EXPORT void free_gpu_memory(int device_id);

#ifdef __cplusplus
}
#endif

#endif // GPU_PROVER_H