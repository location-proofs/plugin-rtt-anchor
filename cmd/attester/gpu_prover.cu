#include "gpu_prover.h"
#include <cuda_runtime.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

static uint64_t *d_vram_buffer = NULL;
static size_t g_total_elements = 0;

#define CHECK_CUDA(call)                                          \
    do                                                            \
    {                                                             \
        cudaError_t err = call;                                   \
        if (err != cudaSuccess)                                   \
        {                                                         \
            fprintf(stderr, "CUDA Error at %s:%d: %s\n",          \
                    __FILE__, __LINE__, cudaGetErrorString(err)); \
            return -1;                                            \
        }                                                         \
    } while (0)

__global__ void init_vram_kernel(uint64_t *buffer, size_t total_elements, uint64_t seed)
{
    size_t tid = (size_t)blockDim.x * blockIdx.x + threadIdx.x;
    size_t stride = (size_t)blockDim.x * gridDim.x;

    for (size_t i = tid; i < total_elements; i += stride)
    {
        uint64_t x = i ^ seed;
        x ^= x >> 12;
        x ^= x << 25;
        x ^= x >> 27;
        buffer[i] = x * 0x2545F4914F6CDD1DULL;
    }
}

__global__ void vram_traverse_kernel(
    const uint64_t *__restrict__ buffer,
    size_t total_elements,
    uint64_t seed,
    uint32_t iterations,
    uint64_t *d_results)
{
    uint32_t tid = blockDim.x * blockIdx.x + threadIdx.x;
    uint64_t state = seed ^ ((uint64_t)tid * 0x9E3779B97F4A7C15ULL);
    uint64_t idx = state % total_elements;

    for (uint32_t i = 0; i < iterations; ++i)
    {
        // Bypass L1 cache to force physical VRAM bus transactions
        uint64_t fetched = __ldcg(&buffer[idx]);
        state ^= fetched;
        state = (state << 13) | (state >> 51);
        state += 0xD6E8FEB86659FD93ULL;
        idx = (idx ^ state) % total_elements;
    }

    d_results[tid] = state;
}

extern "C"
{

    EXPORT int init_gpu_memory(int device_id, size_t size_bytes)
    {
        CHECK_CUDA(cudaSetDevice(device_id));
        if (d_vram_buffer != NULL)
        {
            cudaFree(d_vram_buffer);
            d_vram_buffer = NULL;
        }

        g_total_elements = size_bytes / sizeof(uint64_t);
        CHECK_CUDA(cudaMalloc((void **)&d_vram_buffer, size_bytes));

        int threads = 256;
        int blocks = 1024;
        init_vram_kernel<<<blocks, threads>>>(d_vram_buffer, g_total_elements, 0x517CC1B727220A95ULL);
        CHECK_CUDA(cudaDeviceSynchronize());
        return 0;
    }

    EXPORT int run_gpu_memory_challenge(
        int device_id,
        const uint8_t *challenge_data,
        size_t challenge_len,
        uint8_t *out_digest_32)
    {
        CHECK_CUDA(cudaSetDevice(device_id));
        if (d_vram_buffer == NULL || g_total_elements == 0)
        {
            fprintf(stderr, "GPU memory not initialized\n");
            return -1;
        }

        // Fold incoming challenge into a 64-bit seed
        uint64_t seed = 0xCBF29CE484222325ULL;
        for (size_t i = 0; i < challenge_len; ++i)
        {
            seed ^= (uint64_t)challenge_data[i];
            seed *= 0x100000001B3ULL;
        }

        const int threads = 256;
        const int blocks = 512;
        const int total_threads = threads * blocks;

        uint64_t *d_results = NULL;
        CHECK_CUDA(cudaMalloc((void **)&d_results, total_threads * sizeof(uint64_t)));

        // Run traversal pass across VRAM
        vram_traverse_kernel<<<blocks, threads>>>(
            d_vram_buffer, g_total_elements, seed, 1024, d_results);
        CHECK_CUDA(cudaDeviceSynchronize());

        uint64_t h_results[total_threads];
        CHECK_CUDA(cudaMemcpy(h_results, d_results, total_threads * sizeof(uint64_t), cudaMemcpyDeviceToHost));
        cudaFree(d_results);

        // Final 32-byte digest reduction
        uint64_t digest[4] = {seed, 0x100000001B3ULL, 0xCBF29CE484222325ULL, 0x9E3779B97F4A7C15ULL};
        for (int i = 0; i < total_threads; ++i)
        {
            digest[i % 4] ^= h_results[i];
            digest[(i + 1) % 4] *= 0x100000001B3ULL;
        }

        memcpy(out_digest_32, digest, 32);
        return 0;
    }

    EXPORT void free_gpu_memory(int device_id)
    {
        cudaSetDevice(device_id);
        if (d_vram_buffer != NULL)
        {
            cudaFree(d_vram_buffer);
            d_vram_buffer = NULL;
            g_total_elements = 0;
        }
    }

} // extern "C"