/* msvc/runtime_init.c
 *
 * Starts Go's runtime when the archive is linked by MSVC.
 *
 * -buildmode=c-archive arranges for runtime initialization by putting a
 * pointer to _rt0_amd64_windows_lib in a .ctors section of go.o. That is the
 * GNU convention: mingw's ld collects .ctors and the startup code walks it.
 * MSVC's linker does not know the section -- it uses .CRT$XCU -- so link.exe
 * links the archive without complaint and simply never runs the initializer.
 *
 * The result is a program that builds, links, starts, and then blocks forever
 * inside _cgo_wait_runtime_init_done on the FIRST call into Go, waiting on an
 * init that was never wired up. There is no error message anywhere in that
 * chain. golang/go#42190, open since 2020.
 *
 * This file is the missing wiring: the same function, reached through the
 * section MSVC does honour. Compiled only under MSVC -- with mingw the .ctors
 * entry already works and this would run the initializer a second time.
 *
 * WHY /include: APPEARS TWICE. Nothing references this object, so the linker
 * has no reason to keep it -- and if it is dropped, the initializer goes with
 * it and the deadlock comes back with no new symptom to distinguish it. The
 * #pragma below covers a target that compiles this file directly. It CANNOT
 * cover the static-library case, which is how omegamaps links it: a pragma
 * inside an object the linker never pulled in was never read. For that, the
 * top-level CMakeLists.txt puts /INCLUDE:omegamaps_go_runtime_init on the link
 * line of everything consuming omegamaps::c.
 */

/* Defined in go.o. Spawns the runtime's own thread and returns; it does not
 * block, which is what makes it safe to call from a C initializer. */
extern void _rt0_amd64_windows_lib(void);

static int __cdecl omegamaps_start_go_runtime(void) {
    _rt0_amd64_windows_lib();
    return 0;
}

#pragma section(".CRT$XCU", long, read)

__declspec(allocate(".CRT$XCU"))
int(__cdecl *omegamaps_go_runtime_init)(void) = omegamaps_start_go_runtime;

#pragma comment(linker, "/include:omegamaps_go_runtime_init")
