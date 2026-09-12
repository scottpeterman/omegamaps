# viewer/CMakeLists.txt
#
# omegamaps-viewer: internal/mapweb's page in a QWebEngineView. The window
# code is a library so viewer_probe links the same code as the executable.
#
# The executable is written beside the application (build/app/), because the
# application finds it there: same directory on Linux and Windows, a sibling
# bundle on macOS.

set(CMAKE_AUTOMOC ON)

add_library(omegamaps_viewer STATIC
    mapserver.cpp    mapserver.h
    viewerwindow.cpp viewerwindow.h)
target_include_directories(omegamaps_viewer PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
# omegamaps_ui for the theme: the viewer paints with the application's tokens
# and stylesheet, not a copy of them.
target_link_libraries(omegamaps_viewer PUBLIC omegamaps_ui Qt6::WebEngineWidgets)

add_executable(omegamaps-viewer WIN32 MACOSX_BUNDLE main.cpp)
target_link_libraries(omegamaps-viewer PRIVATE omegamaps_viewer)
set_target_properties(omegamaps-viewer PROPERTIES
    RUNTIME_OUTPUT_DIRECTORY "${PROJECT_BINARY_DIR}/app"
    MACOSX_BUNDLE_BUNDLE_NAME "omegamaps viewer"
    MACOSX_BUNDLE_GUI_IDENTIFIER "com.github.scottpeterman.omegamaps.viewer")
