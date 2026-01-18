# CUDA GPU library Makefile

NVCC = nvcc
CUDA_PATH ?= /usr/local/cuda

# Detect GPU architecture
# Use native architecture for best performance
CUDA_ARCH ?= -arch=native

NVCC_FLAGS = -O3 -Xcompiler -fPIC $(CUDA_ARCH)

# Output library
LIBNAME = libssqlgpu.so

.PHONY: all clean

all: $(LIBNAME)

$(LIBNAME): sum.cu
	$(NVCC) $(NVCC_FLAGS) -shared -o $@ $< -lcudart

clean:
	rm -f $(LIBNAME)

# Install to system (requires sudo)
install: $(LIBNAME)
	cp $(LIBNAME) /usr/local/lib/
	ldconfig

# For development: install to local directory
install-local: $(LIBNAME)
	mkdir -p ../lib
	cp $(LIBNAME) ../lib/
