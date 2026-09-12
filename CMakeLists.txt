# CMakeLists.txt
#
# The Qt application and the C surface it sits on. The command-line tools are
# not built here -- they are pure Go and scripts/build.sh builds them without
# cgo. This build is the other half: the run model as a Go c-archive, and the
# application linked against it, the way omegassh builds Omega.
#
# The Go archive is a custom command, so `cmake --build` is the only entry
# point. Every environment variable the Go toolchain reads (GOFLAGS, GOPROXY,
# GOTOOLCHAIN, ...) comes from the shell that runs the build, not from the
# configure step -- see docs/BUILDING.md.

cmake_minimum_required(VERSION 3.19)
project(omegamaps VERSION 0.1.0 LANGUAGES C CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)

option(OMEGAMAPS_BUILD_APP "Build the Qt application" ON)
option(OMEGAMAPS_BUILD_TESTS "Build the replay probe" ON)
option(OMEGAMAPS_BUILD_VIEWER "Build the map viewer (needs Qt WebEngine)" ON)

find_program(GO_EXECUTABLE go REQUIRED)

# The generated header's name follows -o, not the platform. See the matching
# block in omegassh's CMakeLists.txt for what naming it wrongly costs on MSBuild.
if(WIN32)
    set(OMEGAMAPS_ARCHIVE "${CMAKE_CURRENT_BINARY_DIR}/omegamaps.lib")
    set(OMEGAMAPS_GENERATED_HEADER "${CMAKE_CURRENT_BINARY_DIR}/omegamaps.h")
else()
    set(OMEGAMAPS_ARCHIVE "${CMAKE_CURRENT_BINARY_DIR}/libomegamaps.a")
    set(OMEGAMAPS_GENERATED_HEADER "${CMAKE_CURRENT_BINARY_DIR}/libomegamaps.h")
endif()

# Windows: mingw-w64 compiles the archive, MSVC links it. The flags and the
# three MSVC seams below are carried over from omegassh unchanged -- they are
# properties of a Go c-archive meeting link.exe, not of either project. Each
# one's full reasoning is in omegassh's CMakeLists.txt and docs/WINDOWS.md.
#   -D__USE_MINGW_ANSI_STDIO=0  cgo's printf calls resolve against the UCRT
#   -gdwarf-4                   gcc 12+ DWARF 5 is implicated in images the
#                               loader refuses with 0xc000007b
# CGO_CFLAGS is replaced, not extended, so -O2 -g are restated.
if(WIN32)
    set(OMEGAMAPS_GO_ENV CGO_ENABLED=1
        "CGO_CFLAGS=-O2 -g -gdwarf-4 -D__USE_MINGW_ANSI_STDIO=0")
else()
    set(OMEGAMAPS_GO_ENV CGO_ENABLED=1)
endif()

# MSVC rejects go.o when its .pdata records are out of address order, and Go's
# linker does not promise the order (LNK1223). scripts/pdatafix sorts them in
# place; a no-op on an archive that is already sorted.
if(MSVC)
    set(OMEGAMAPS_PDATA_FIX
        COMMAND ${GO_EXECUTABLE} run ./scripts/pdatafix ${OMEGAMAPS_ARCHIVE})
else()
    set(OMEGAMAPS_PDATA_FIX "")
endif()

# Every Go file under capi/ and internal/, recursively: the archive links most
# of the module, and a dependency missed here is a stale archive that builds
# cleanly and runs old code.
file(GLOB_RECURSE OMEGAMAPS_GO_SOURCES CONFIGURE_DEPENDS
    "${CMAKE_CURRENT_SOURCE_DIR}/capi/*.go"
    "${CMAKE_CURRENT_SOURCE_DIR}/internal/*.go"
    "${CMAKE_CURRENT_SOURCE_DIR}/internal/*.textfsm"
    "${CMAKE_CURRENT_SOURCE_DIR}/internal/*.json"
    # The map viewer's page is go:embed'ed into the archive (internal/mapweb/
    # assets). Without these an edit to the page builds nothing and the
    # viewer serves the previous one.
    "${CMAKE_CURRENT_SOURCE_DIR}/internal/*.js"
    "${CMAKE_CURRENT_SOURCE_DIR}/internal/*.html"
    "${CMAKE_CURRENT_SOURCE_DIR}/internal/*.css"
    "${CMAKE_CURRENT_SOURCE_DIR}/internal/*.svg")
list(APPEND OMEGAMAPS_GO_SOURCES
    "${CMAKE_CURRENT_SOURCE_DIR}/go.mod")

add_custom_command(
    OUTPUT ${OMEGAMAPS_ARCHIVE} ${OMEGAMAPS_GENERATED_HEADER}
    COMMAND ${CMAKE_COMMAND} -E env ${OMEGAMAPS_GO_ENV}
            ${GO_EXECUTABLE} build -buildmode=c-archive -trimpath
            -o ${OMEGAMAPS_ARCHIVE} ./capi
    ${OMEGAMAPS_PDATA_FIX}
    WORKING_DIRECTORY ${CMAKE_CURRENT_SOURCE_DIR}
    DEPENDS ${OMEGAMAPS_GO_SOURCES}
    COMMENT "Building Go c-archive (libomegamaps)"
    VERBATIM)

add_custom_target(omegamaps_archive DEPENDS ${OMEGAMAPS_ARCHIVE})

# The C surface. Consumers include <omegamaps/omegamaps.h>; the cgo header in
# the build directory is an artifact, not the contract.
add_library(omegamaps_c INTERFACE)
add_dependencies(omegamaps_c omegamaps_archive)
target_include_directories(omegamaps_c INTERFACE
    "${CMAKE_CURRENT_SOURCE_DIR}/include")
target_link_libraries(omegamaps_c INTERFACE ${OMEGAMAPS_ARCHIVE})

if(UNIX AND NOT APPLE)
    target_link_libraries(omegamaps_c INTERFACE pthread)
elseif(APPLE)
    # crypto/x509, reached through x/crypto/ssh, imports symbols straight from
    # Security.framework on darwin. A Go binary resolves those in Go's own
    # link; here CMake does the final link, so they have to be on its line or
    # the application fails with undefined Sec* symbols. go-keyring is not the
    # reason -- on darwin it runs /usr/bin/security and links nothing.
    target_link_libraries(omegamaps_c INTERFACE
        "-framework CoreFoundation" "-framework Security")
elseif(WIN32)
    target_link_libraries(omegamaps_c INTERFACE ws2_32 winmm ntdll bcrypt)
    if(MSVC)
        # The UCRT inlines fprintf and friends; cgo's mingw-built objects call
        # them out of line. This library supplies those definitions.
        target_link_libraries(omegamaps_c INTERFACE legacy_stdio_definitions)

        # c-archive starts Go's runtime from .ctors, which link.exe ignores:
        # without this the first call into Go blocks forever with no
        # diagnostic (golang/go#42190). msvc/runtime_init.c calls the same
        # initializer through .CRT$XCU, and /INCLUDE: keeps its object from
        # being dropped out of the static library.
        add_library(omegamaps_msvc_init STATIC msvc/runtime_init.c)
        add_dependencies(omegamaps_msvc_init omegamaps_archive)
        target_link_libraries(omegamaps_c INTERFACE omegamaps_msvc_init)
        target_link_options(omegamaps_c INTERFACE
            "/INCLUDE:omegamaps_go_runtime_init")
    endif()
endif()

add_library(omegamaps::c ALIAS omegamaps_c)

if(OMEGAMAPS_BUILD_APP)
    add_subdirectory(app)

    # The viewer is the one part that needs Qt WebEngine, which not every Qt
    # has (distribution packages split it out; the MinGW kits on Windows lack
    # it). Without it the application builds as before and says, when asked
    # to open a map, that the viewer is not installed.
    set(OMEGAMAPS_HAVE_VIEWER OFF)
    if(OMEGAMAPS_BUILD_VIEWER)
        find_package(Qt6 6.2 QUIET COMPONENTS WebEngineWidgets)
        if(Qt6WebEngineWidgets_FOUND)
            add_subdirectory(viewer)
            set(OMEGAMAPS_HAVE_VIEWER ON)
        else()
            # WebEngine must belong to the Qt in use (Qt6_DIR); a package for
            # another Qt on the machine is not found here, and must not be.
            #
            # It is also not self-contained: it needs Qt Quick (qtdeclarative,
            # a base archive an aqt --archives qtbase install leaves out),
            # QuickWidgets, WebChannel and Positioning. With WebEngine present
            # and one of those missing, QUIET hid the reason and the warning
            # said to install WebEngine. Qt's own reason is kept, and the
            # missing modules are named.
            get_filename_component(_om_qt_cmake "${Qt6_DIR}" DIRECTORY)
            set(_om_missing "")
            foreach(_m WebEngineWidgets WebEngineCore WebChannel Positioning Quick QuickWidgets Qml PrintSupport)
                if(NOT EXISTS "${_om_qt_cmake}/Qt6${_m}/Qt6${_m}Config.cmake")
                    list(APPEND _om_missing "Qt6${_m}")
                endif()
            endforeach()
            set(_om_why "${Qt6WebEngineWidgets_NOT_FOUND_MESSAGE}")
            if(NOT _om_why)
                set(_om_why "${Qt6_NOT_FOUND_MESSAGE}")
            endif()
            if(_om_missing)
                string(REPLACE ";" ", " _om_missing "${_om_missing}")
                set(_om_what "missing from this Qt: ${_om_missing}")
            else()
                set(_om_what "every module is present; Qt's reason follows")
            endif()
            message(WARNING "omegamaps-viewer will not be built: Qt WebEngine is not usable in the Qt at "
                            "${_om_qt_cmake} (${_om_what}).\n${_om_why}\n"
                            "WebEngine, WebChannel, Positioning and Qt Quick must all come from that Qt. "
                            "aqt: --archives qtdeclarative -m qtwebengine qtwebchannel qtpositioning; "
                            "Qt installer: Additional Libraries; distribution Qt: qt6-webengine-dev. "
                            "scripts/build-app.sh prints the exact command.")
        endif()
    endif()

    if(OMEGAMAPS_BUILD_TESTS)
        add_subdirectory(tests)
    endif()
endif()
