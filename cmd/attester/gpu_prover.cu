#include "gpu_prover.h"
#include <cuda_runtime.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

static uint64_t *d_vram_buffer = NULL;
static size_t g_total_elements = 0;
static size_t g_buffer_size_bytes = 0;
static int g_enable_logging = 1; // Enabled by default

#define CHECK_CUDA(call)                                          \
    do                                                            \
    {                                                             \
        cudaError_t err = call;                                   \
        if (err != cudaSuccess)                                   \
        {                                                         \
            fprintf(stderr, "[GPU Error] %s:%d: %s\n",            \
                    __FILE__, __LINE__, cudaGetErrorString(err)); \
            return -1;                                            \
        }                                                         \
    } while (0)

// Conditional logging macro
#define GPU_LOG(...)             \
    do                           \
    {                            \
        if (g_enable_logging)    \
        {                        \
            printf(__VA_ARGS__); \
        }                        \
    } while (0)

// Kernel 1: Fills allocated VRAM buffer with deterministic pseudorandom data
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

// Kernel 2: Memory-hard traversal forcing non-sequential reads from physical VRAM
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
        // __ldcg: Load Cache Global - bypasses L1 cache to hit physical VRAM bus
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
    // Toggle logging dynamically from Go/C
    EXPORT void set_gpu_logging(int enabled)
    {
        g_enable_logging = enabled;
    }

    EXPORT int init_gpu_memory(int device_id, size_t size_bytes)
    {
        CHECK_CUDA(cudaSetDevice(device_id));

        cudaDeviceProp prop;
        CHECK_CUDA(cudaGetDeviceProperties(&prop, device_id));

        GPU_LOG("\n======================================================\n");
        GPU_LOG("[GPU Init] Device %d: %s\n", device_id, prop.name);
        GPU_LOG("[GPU Init] Total Device VRAM: %.2f GB\n", (double)prop.totalGlobalMem / (1024.0 * 1024.0 * 1024.0));
        GPU_LOG("[GPU Init] Requested Buffer Size: %.2f GB (%zu bytes)\n",
                (double)size_bytes / (1024.0 * 1024.0 * 1024.0), size_bytes);

        if (d_vram_buffer != NULL)
        {
            GPU_LOG("[GPU Init] Freeing previously allocated VRAM buffer...\n");
            cudaFree(d_vram_buffer);
            d_vram_buffer = NULL;
        }

        g_buffer_size_bytes = size_bytes;
        g_total_elements = size_bytes / sizeof(uint64_t);

        GPU_LOG("[GPU Init] Allocating %.2f GB buffer on GPU...\n", (double)size_bytes / (1024.0 * 1024.0 * 1024.0));
        CHECK_CUDA(cudaMalloc((void **)&d_vram_buffer, size_bytes));

        int threads = 256;
        int blocks = 1024;
        GPU_LOG("[GPU Init] Populating %zu uint64 elements with pseudo-random seed...\n", g_total_elements);

        cudaEvent_t start, stop;
        CHECK_CUDA(cudaEventCreate(&start));
        CHECK_CUDA(cudaEventCreate(&stop));

        CHECK_CUDA(cudaEventRecord(start, 0));
        init_vram_kernel<<<blocks, threads>>>(d_vram_buffer, g_total_elements, 0x517CC1B727220A95ULL);
        CHECK_CUDA(cudaEventRecord(stop, 0));
        CHECK_CUDA(cudaEventSynchronize(stop));

        float elapsed_ms = 0.0f;
        CHECK_CUDA(cudaEventElapsedTime(&elapsed_ms, start, stop));
        GPU_LOG("[GPU Init] Memory initialization completed in %.2f ms\n", elapsed_ms);
        GPU_LOG("======================================================\n\n");

        cudaEventDestroy(start);
        cudaEventDestroy(stop);
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
            fprintf(stderr, "[GPU Error] VRAM buffer uninitialized. Call init_gpu_memory first.\n");
            return -1;
        }

        GPU_LOG("\n------------------------------------------------------\n");
        GPU_LOG("[GPU Challenge] Received challenge input (%zu bytes): ", challenge_len);
        if (g_enable_logging)
        {
            for (size_t i = 0; i < (challenge_len < 16 ? challenge_len : 16); ++i)
            {
                printf("%02x ", challenge_data[i]);
            }
            if (challenge_len > 16)
                printf("...");
            printf("\n");
        }

        uint64_t seed = 0xCBF29CE484222325ULL;
        for (size_t i = 0; i < challenge_len; ++i)
        {
            seed ^= (uint64_t)challenge_data[i];
            seed *= 0x100000001B3ULL;
        }
        GPU_LOG("[GPU Challenge] Computed initial seed: 0x%016llx\n", (unsigned long long)seed);

        const int threads = 256;
        const int blocks = 512;
        const int total_threads = threads * blocks;
        const uint32_t iterations = 1024;
        const size_t total_lookups = (size_t)total_threads * (size_t)iterations;
        const double total_data_read_gb = (double)(total_lookups * sizeof(uint64_t)) / (1024.0 * 1024.0 * 1024.0);

        GPU_LOG("[GPU Challenge] Launching %d blocks x %d threads (%d parallel threads)\n", blocks, threads, total_threads);
        GPU_LOG("[GPU Challenge] Total random VRAM reads: %zu (%.2f GB non-cached traffic)\n", total_lookups, total_data_read_gb);

        uint64_t *d_results = NULL;
        CHECK_CUDA(cudaMalloc((void **)&d_results, total_threads * sizeof(uint64_t)));

        cudaEvent_t start, stop;
        CHECK_CUDA(cudaEventCreate(&start));
        CHECK_CUDA(cudaEventCreate(&stop));

        CHECK_CUDA(cudaEventRecord(start, 0));
        vram_traverse_kernel<<<blocks, threads>>>(d_vram_buffer, g_total_elements, seed, iterations, d_results);
        CHECK_CUDA(cudaEventRecord(stop, 0));
        CHECK_CUDA(cudaEventSynchronize(stop));

        float kernel_ms = 0.0f;
        CHECK_CUDA(cudaEventElapsedTime(&kernel_ms, start, stop));

        double effective_bandwidth = (total_data_read_gb / (kernel_ms / 1000.0));
        GPU_LOG("[GPU Challenge] Kernel execution time: %.3f ms (Effective bandwidth: %.2f GB/s)\n",
                kernel_ms, effective_bandwidth);

        uint64_t *h_results = (uint64_t *)malloc(total_threads * sizeof(uint64_t));
        if (!h_results)
        {
            fprintf(stderr, "[GPU Error] Host memory allocation failed\n");
            cudaFree(d_results);
            return -1;
        }

        CHECK_CUDA(cudaMemcpy(h_results, d_results, total_threads * sizeof(uint64_t), cudaMemcpyDeviceToHost));
        cudaFree(d_results);

        uint64_t digest[4] = {seed, 0x100000001B3ULL, 0xCBF29CE484222325ULL, 0x9E3779B97F4A7C15ULL};
        for (int i = 0; i < total_threads; ++i)
        {
            digest[i % 4] ^= h_results[i];
            digest[(i + 1) % 4] *= 0x100000001B3ULL;
        }
        free(h_results);

        memcpy(out_digest_32, digest, 32);

        GPU_LOG("[GPU Challenge] Produced 32-byte proof digest: ");
        if (g_enable_logging)
        {
            for (int i = 0; i < 32; ++i)
            {
                printf("%02x", out_digest_32[i]);
            }
            printf("\n");
        }
        GPU_LOG("------------------------------------------------------\n\n");

        cudaEventDestroy(start);
        cudaEventDestroy(stop);
        return 0;
    }

    EXPORT void free_gpu_memory(int device_id)
    {
        cudaSetDevice(device_id);
        if (d_vram_buffer != NULL)
        {
            GPU_LOG("[GPU Cleanup] Freeing %.2f GB VRAM on Device %d\n",
                    (double)g_buffer_size_bytes / (1024.0 * 1024.0 * 1024.0), device_id);
            cudaFree(d_vram_buffer);
            d_vram_buffer = NULL;
            g_total_elements = 0;
            g_buffer_size_bytes = 0;
        }
    }
}