# Compiles for Turing (RTX 20/T4), Ampere (RTX 30/A100), and Ada (RTX 40/L4)
nvcc -O3 -c gpu_signer.cu -o gpu_signer.o \
  -gencode arch=compute_86,code=sm_86 \
  --compiler-options '-fPIC'

# Create the static library again
ar rcs libgpusigner.a gpu_signer.o

export CGO_CFLAGS="-I."
CGO_LDFLAGS="-L. -lgpusigner -L/usr/local/cuda/lib64 -lcudart_static -lrt -ldl -lstdc++"
 go build -o attester main.go