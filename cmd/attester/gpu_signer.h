#ifndef GPU_SIGNER_H
#define GPU_SIGNER_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C"
{
#endif

    int gpu_init_context(int device_id);

    int gpu_ed25519_sign(
        int device_id,
        const uint8_t *priv_key,
        const uint8_t *msg,
        size_t msg_len,
        uint8_t *out_sig);

#ifdef __cplusplus
}
#endif

#endif // GPU_SIGNER_H