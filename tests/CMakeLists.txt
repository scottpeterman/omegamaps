# tests/CMakeLists.txt
#
# replay_probe plays a recording through the real window with no display and
# checks the views against the run model. crawl_probe runs a real crawl of the
# fake lab through the C surface and the window. See each file for what it
# asserts.

add_executable(replay_probe replay_probe.cpp)
target_link_libraries(replay_probe PRIVATE omegamaps_ui)

# fakelab is the fake lab crawl_probe crawls: a Go program, built here so it
# sits beside the probe. Pure Go -- no cgo -- and never shipped.
set(OMEGAMAPS_FAKELAB "${CMAKE_CURRENT_BINARY_DIR}/fakelab${CMAKE_EXECUTABLE_SUFFIX}")
file(GLOB OMEGAMAPS_FAKELAB_SOURCES CONFIGURE_DEPENDS
    "${PROJECT_SOURCE_DIR}/tests/fakelab/*.go"
    "${PROJECT_SOURCE_DIR}/internal/fakedev/*.go"
    "${PROJECT_SOURCE_DIR}/internal/sshcore/*.go")
add_custom_command(
    OUTPUT ${OMEGAMAPS_FAKELAB}
    COMMAND ${CMAKE_COMMAND} -E env CGO_ENABLED=0
            ${GO_EXECUTABLE} build -trimpath -o ${OMEGAMAPS_FAKELAB} ./tests/fakelab
    WORKING_DIRECTORY ${PROJECT_SOURCE_DIR}
    DEPENDS ${OMEGAMAPS_FAKELAB_SOURCES} "${PROJECT_SOURCE_DIR}/go.mod"
    COMMENT "Building the fake lab"
    VERBATIM)
add_custom_target(fakelab DEPENDS ${OMEGAMAPS_FAKELAB})

add_executable(crawl_probe crawl_probe.cpp)
target_link_libraries(crawl_probe PRIVATE omegamaps_ui)
add_dependencies(crawl_probe fakelab)

# viewer_probe drives the map viewer window offscreen: page, theme, exports,
# layouts. Only where the viewer is built.
if(OMEGAMAPS_HAVE_VIEWER)
    add_executable(viewer_probe viewer_probe.cpp)
    target_link_libraries(viewer_probe PRIVATE omegamaps_viewer)
    add_dependencies(viewer_probe omegamaps-viewer)
endif()
