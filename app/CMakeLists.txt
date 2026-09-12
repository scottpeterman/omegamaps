# app/CMakeLists.txt
#
# The views as a library, so the application and the replay probe link the
# same code; then the application itself.
#
# Qt 6.2 is the floor, as for omegassh: nothing here uses qt_standard_project_setup()
# or the other 6.3+ helpers. Svg is for the device icons (QSvgRenderer, so the
# bundle needs libQt6Svg and no image-format or icon-engine plugin).

find_package(Qt6 6.2 REQUIRED COMPONENTS Widgets Svg)

set(CMAKE_AUTOMOC ON)
set(CMAKE_AUTORCC ON)

add_library(omegamaps_ui STATIC
    aboutdialog.cpp   aboutdialog.h
    crawlform.cpp     crawlform.h
    discoverylog.cpp  discoverylog.h
    filedialogs.cpp   filedialogs.h
    pathprompt.cpp    pathprompt.h
    icons.cpp         icons.h
    mainwindow.cpp    mainwindow.h
    panel.cpp         panel.h
    progresspanel.cpp progresspanel.h
    runbridge.cpp     runbridge.h
    runtypes.cpp      runtypes.h
    theme.cpp         theme.h
    vault.cpp         vault.h
    vaultdialogs.cpp  vaultdialogs.h
    viewerlaunch.cpp  viewerlaunch.h
    topologypreview.cpp topologypreview.h
    widgets.cpp       widgets.h
    resources.qrc)
target_include_directories(omegamaps_ui PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
target_link_libraries(omegamaps_ui PUBLIC Qt6::Widgets Qt6::Svg omegamaps::c)

add_executable(omegamaps WIN32 MACOSX_BUNDLE main.cpp)
target_link_libraries(omegamaps PRIVATE omegamaps_ui)
