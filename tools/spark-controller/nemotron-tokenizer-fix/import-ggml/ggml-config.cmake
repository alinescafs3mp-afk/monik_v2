# Import-only package for the exact installed df18bee runtime. No ggml/CUDA build.
# Configure the copied llama source with LLAMA_USE_SYSTEM_GGML=ON and ggml_DIR here.
if(NOT DEFINED NEMO_PINNED_SOURCE OR NOT DEFINED NEMO_PINNED_GGML_BIN)
    message(FATAL_ERROR "Set isolated NEMO_PINNED_SOURCE and exact NEMO_PINNED_GGML_BIN")
endif()
set(_nemo_ggml_names ggml-base ggml-cpu ggml-cuda ggml)
set(_nemo_ggml_hashes
    67b579fdc85c87ecab3adc52e1bdf5548189684e9904a26223d5d2617be0d5e7
    b98b98a45d476a04fbea9b04020e00372fbb1b47abecaed9045211cf1f0dfbf2
    1ef39091979e4093b1089535ca1db34a066bf2789a6ebac54892d8a95ad8aef4
    24b18e5727b4338e62815210df0d970f4b28eb27949bad8b2bf325410e8fcc03)
foreach(_i RANGE 0 3)
    list(GET _nemo_ggml_names ${_i} _name)
    list(GET _nemo_ggml_hashes ${_i} _expected)
    set(_path "${NEMO_PINNED_GGML_BIN}/lib${_name}.so.0.26.0")
    if(NOT EXISTS "${_path}")
        message(FATAL_ERROR "Missing pinned library ${_path}")
    endif()
    file(SHA256 "${_path}" _actual)
    if(NOT _actual STREQUAL _expected)
        message(FATAL_ERROR "Pinned library mismatch: ${_path}")
    endif()
    add_library(ggml::${_name} SHARED IMPORTED GLOBAL)
    set_target_properties(ggml::${_name} PROPERTIES
        IMPORTED_LOCATION "${_path}"
        INTERFACE_INCLUDE_DIRECTORIES "${NEMO_PINNED_SOURCE}/ggml/include"
        INTERFACE_COMPILE_DEFINITIONS "GGML_SHARED")
endforeach()
set_target_properties(ggml::ggml-cpu PROPERTIES
    INTERFACE_LINK_LIBRARIES "ggml::ggml-base"
    INTERFACE_COMPILE_DEFINITIONS "GGML_SHARED;GGML_BACKEND_SHARED")
set_target_properties(ggml::ggml-cuda PROPERTIES
    INTERFACE_LINK_LIBRARIES "ggml::ggml-base"
    INTERFACE_COMPILE_DEFINITIONS "GGML_SHARED;GGML_BACKEND_SHARED")
set_target_properties(ggml::ggml PROPERTIES
    INTERFACE_LINK_LIBRARIES "ggml::ggml-base;ggml::ggml-cpu;ggml::ggml-cuda"
    INTERFACE_COMPILE_DEFINITIONS "GGML_SHARED;GGML_USE_CPU;GGML_USE_CUDA")
set(ggml_FOUND TRUE)
set(ggml_VERSION 0.26.0)
