// Copyright 2015 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package java

// This file contains the module types for compiling Java for Android, and converts the properties
// into the flags and filenames necessary to pass to the Module.  The final creation of the rules
// is handled in builder.go

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/google/blueprint"
	"github.com/google/blueprint/pathtools"
	"github.com/google/blueprint/proptools"

	"android/soong/android"
	"android/soong/dexpreopt"
	"android/soong/java/config"
	"android/soong/tradefed"
)

func init() {
	RegisterJavaBuildComponents(android.InitRegistrationContext)

	// Register sdk member types.
	android.RegisterSdkMemberType(javaHeaderLibsSdkMemberType)

	android.RegisterSdkMemberType(&librarySdkMemberType{
		android.SdkMemberTypeBase{
			PropertyName: "java_libs",
		},
		func(j *Library) android.Path {
			implementationJars := j.ImplementationJars()
			if len(implementationJars) != 1 {
				panic(fmt.Errorf("there must be only one implementation jar from %q", j.Name()))
			}

			return implementationJars[0]
		},
	})

	android.RegisterSdkMemberType(&testSdkMemberType{
		SdkMemberTypeBase: android.SdkMemberTypeBase{
			PropertyName: "java_tests",
		},
	})

	android.PostDepsMutators(RegisterPostDepsMutators)
}

func RegisterJavaBuildComponents(ctx android.RegistrationContext) {
	ctx.RegisterModuleType("java_defaults", DefaultsFactory)

	ctx.RegisterModuleType("java_library", LibraryFactory)
	ctx.RegisterModuleType("java_library_static", LibraryStaticFactory)
	ctx.RegisterModuleType("java_library_host", LibraryHostFactory)
	ctx.RegisterModuleType("java_binary", BinaryFactory)
	ctx.RegisterModuleType("java_binary_host", BinaryHostFactory)
	ctx.RegisterModuleType("java_test", TestFactory)
	ctx.RegisterModuleType("java_test_helper_library", TestHelperLibraryFactory)
	ctx.RegisterModuleType("java_test_host", TestHostFactory)
	ctx.RegisterModuleType("java_test_import", JavaTestImportFactory)
	ctx.RegisterModuleType("java_import", ImportFactory)
	ctx.RegisterModuleType("java_import_host", ImportFactoryHost)
	ctx.RegisterModuleType("java_device_for_host", DeviceForHostFactory)
	ctx.RegisterModuleType("java_host_for_device", HostForDeviceFactory)
	ctx.RegisterModuleType("dex_import", DexImportFactory)

	ctx.FinalDepsMutators(func(ctx android.RegisterMutatorsContext) {
		ctx.BottomUp("dexpreopt_tool_deps", dexpreoptToolDepsMutator).Parallel()
	})

	ctx.RegisterSingletonType("logtags", LogtagsSingleton)
	ctx.RegisterSingletonType("kythe_java_extract", kytheExtractJavaFactory)
}

func RegisterPostDepsMutators(ctx android.RegisterMutatorsContext) {
	ctx.BottomUp("ordered_system_server_jars", systemServerJarsDepsMutator)
}

var (
	dexpreoptedSystemServerJarsKey  = android.NewOnceKey("dexpreoptedSystemServerJars")
	dexpreoptedSystemServerJarsLock sync.Mutex
)

func DexpreoptedSystemServerJars(config android.Config) *[]string {
	return config.Once(dexpreoptedSystemServerJarsKey, func() interface{} {
		return &[]string{}
	}).(*[]string)
}

// A PostDepsMutator pass that enforces total order on non-updatable system server jars. A total
// order is neededed because such jars must be dexpreopted together (each jar on the list must have
// all preceding jars in its class loader context). The total order must be compatible with the
// partial order imposed by genuine dependencies between system server jars (which is not always
// respected by the PRODUCT_SYSTEM_SERVER_JARS variable).
//
// An earlier mutator pass creates genuine dependencies, and this pass traverses the jars in that
// order (which is partial and non-deterministic). This pass adds additional dependencies between
// jars, making the order total and deterministic. It also constructs a global ordered list.
func systemServerJarsDepsMutator(ctx android.BottomUpMutatorContext) {
	jars := dexpreopt.NonUpdatableSystemServerJars(ctx, dexpreopt.GetGlobalConfig(ctx))
	name := ctx.ModuleName()
	if android.InList(name, jars) {
		dexpreoptedSystemServerJarsLock.Lock()
		defer dexpreoptedSystemServerJarsLock.Unlock()
		jars := DexpreoptedSystemServerJars(ctx.Config())
		for _, dep := range *jars {
			ctx.AddDependency(ctx.Module(), dexpreopt.SystemServerDepTag, dep)
		}
		*jars = append(*jars, name)
	}
}

func (j *Module) checkSdkVersion(ctx android.ModuleContext) {
	if j.SocSpecific() || j.DeviceSpecific() ||
		(j.ProductSpecific() && ctx.Config().EnforceProductPartitionInterface()) {
		if sc, ok := ctx.Module().(sdkContext); ok {
			if !sc.sdkVersion().specified() {
				ctx.PropertyErrorf("sdk_version",
					"sdk_version must have a value when the module is located at vendor or product(only if PRODUCT_ENFORCE_PRODUCT_PARTITION_INTERFACE is set).")
			}
		}
	}
}

func (j *Module) checkPlatformAPI(ctx android.ModuleContext) {
	if sc, ok := ctx.Module().(sdkContext); ok {
		usePlatformAPI := proptools.Bool(j.deviceProperties.Platform_apis)
		sdkVersionSpecified := sc.sdkVersion().specified()
		if usePlatformAPI && sdkVersionSpecified {
			ctx.PropertyErrorf("platform_apis", "platform_apis must be false when sdk_version is not empty.")
		} else if !usePlatformAPI && !sdkVersionSpecified {
			ctx.PropertyErrorf("platform_apis", "platform_apis must be true when sdk_version is empty.")
		}

	}
}

// TODO:
// Autogenerated files:
//  Renderscript
// Post-jar passes:
//  Proguard
// Rmtypedefs
// DroidDoc
// Findbugs

type CompilerProperties struct {
	// list of source files used to compile the Java module.  May be .java, .logtags, .proto,
	// or .aidl files.
	Srcs []string `android:"path,arch_variant"`

	// list of source files that should not be used to build the Java module.
	// This is most useful in the arch/multilib variants to remove non-common files
	Exclude_srcs []string `android:"path,arch_variant"`

	// list of directories containing Java resources
	Java_resource_dirs []string `android:"arch_variant"`

	// list of directories that should be excluded from java_resource_dirs
	Exclude_java_resource_dirs []string `android:"arch_variant"`

	// list of files to use as Java resources
	Java_resources []string `android:"path,arch_variant"`

	// list of files that should be excluded from java_resources and java_resource_dirs
	Exclude_java_resources []string `android:"path,arch_variant"`

	// list of module-specific flags that will be used for javac compiles
	Javacflags []string `android:"arch_variant"`

	// list of module-specific flags that will be used for kotlinc compiles
	Kotlincflags []string `android:"arch_variant"`

	// list of of java libraries that will be in the classpath
	Libs []string `android:"arch_variant"`

	// list of java libraries that will be compiled into the resulting jar
	Static_libs []string `android:"arch_variant"`

	// manifest file to be included in resulting jar
	Manifest *string `android:"path"`

	// if not blank, run jarjar using the specified rules file
	Jarjar_rules *string `android:"path,arch_variant"`

	// If not blank, set the java version passed to javac as -source and -target
	Java_version *string

	// If set to true, allow this module to be dexed and installed on devices.  Has no
	// effect on host modules, which are always considered installable.
	Installable *bool

	// If set to true, include sources used to compile the module in to the final jar
	Include_srcs *bool

	// If not empty, classes are restricted to the specified packages and their sub-packages.
	// This restriction is checked after applying jarjar rules and including static libs.
	Permitted_packages []string

	// List of modules to use as annotation processors
	Plugins []string

	// List of modules to export to libraries that directly depend on this library as annotation processors
	Exported_plugins []string

	// The number of Java source entries each Javac instance can process
	Javac_shard_size *int64

	// Add host jdk tools.jar to bootclasspath
	Use_tools_jar *bool

	Openjdk9 struct {
		// List of source files that should only be used when passing -source 1.9 or higher
		Srcs []string `android:"path"`

		// List of javac flags that should only be used when passing -source 1.9 or higher
		Javacflags []string
	}

	// When compiling language level 9+ .java code in packages that are part of
	// a system module, patch_module names the module that your sources and
	// dependencies should be patched into. The Android runtime currently
	// doesn't implement the JEP 261 module system so this option is only
	// supported at compile time. It should only be needed to compile tests in
	// packages that exist in libcore and which are inconvenient to move
	// elsewhere.
	Patch_module *string `android:"arch_variant"`

	Jacoco struct {
		// List of classes to include for instrumentation with jacoco to collect coverage
		// information at runtime when building with coverage enabled.  If unset defaults to all
		// classes.
		// Supports '*' as the last character of an entry in the list as a wildcard match.
		// If preceded by '.' it matches all classes in the package and subpackages, otherwise
		// it matches classes in the package that have the class name as a prefix.
		Include_filter []string

		// List of classes to exclude from instrumentation with jacoco to collect coverage
		// information at runtime when building with coverage enabled.  Overrides classes selected
		// by the include_filter property.
		// Supports '*' as the last character of an entry in the list as a wildcard match.
		// If preceded by '.' it matches all classes in the package and subpackages, otherwise
		// it matches classes in the package that have the class name as a prefix.
		Exclude_filter []string
	}

	Errorprone struct {
		// List of javac flags that should only be used when running errorprone.
		Javacflags []string
	}

	Proto struct {
		// List of extra options that will be passed to the proto generator.
		Output_params []string
	}

	Instrument bool `blueprint:"mutated"`

	// List of files to include in the META-INF/services folder of the resulting jar.
	Services []string `android:"path,arch_variant"`
}

type CompilerDeviceProperties struct {
	// list of module-specific flags that will be used for dex compiles
	Dxflags []string `android:"arch_variant"`

	// if not blank, set to the version of the sdk to compile against.
	// Defaults to compiling against the current platform.
	Sdk_version *string

	// if not blank, set the minimum version of the sdk that the compiled artifacts will run against.
	// Defaults to sdk_version if not set.
	Min_sdk_version *string

	// if not blank, set the targetSdkVersion in the AndroidManifest.xml.
	// Defaults to sdk_version if not set.
	Target_sdk_version *string

	// Whether to compile against the platform APIs instead of an SDK.
	// If true, then sdk_version must be empty. The value of this field
	// is ignored when module's type isn't android_app.
	Platform_apis *bool

	Aidl struct {
		// Top level directories to pass to aidl tool
		Include_dirs []string

		// Directories rooted at the Android.bp file to pass to aidl tool
		Local_include_dirs []string

		// directories that should be added as include directories for any aidl sources of modules
		// that depend on this module, as well as to aidl for this module.
		Export_include_dirs []string

		// whether to generate traces (for systrace) for this interface
		Generate_traces *bool

		// whether to generate Binder#GetTransaction name method.
		Generate_get_transaction_name *bool
	}

	// If true, export a copy of the module as a -hostdex module for host testing.
	Hostdex *bool

	Target struct {
		Hostdex struct {
			// Additional required dependencies to add to -hostdex modules.
			Required []string
		}
	}

	// If set to true, compile dex regardless of installable.  Defaults to false.
	Compile_dex *bool

	Optimize struct {
		// If false, disable all optimization.  Defaults to true for android_app and android_test
		// modules, false for java_library and java_test modules.
		Enabled *bool
		// True if the module containing this has it set by default.
		EnabledByDefault bool `blueprint:"mutated"`

		// If true, optimize for size by removing unused code.  Defaults to true for apps,
		// false for libraries and tests.
		Shrink *bool

		// If true, optimize bytecode.  Defaults to false.
		Optimize *bool

		// If true, obfuscate bytecode.  Defaults to false.
		Obfuscate *bool

		// If true, do not use the flag files generated by aapt that automatically keep
		// classes referenced by the app manifest.  Defaults to false.
		No_aapt_flags *bool

		// Flags to pass to proguard.
		Proguard_flags []string

		// Specifies the locations of files containing proguard flags.
		Proguard_flags_files []string `android:"path"`
	}

	// When targeting 1.9 and above, override the modules to use with --system,
	// otherwise provides defaults libraries to add to the bootclasspath.
	System_modules *string

	// set the name of the output
	Stem *string

	UncompressDex bool `blueprint:"mutated"`
	IsSDKLibrary  bool `blueprint:"mutated"`
}

func (me *CompilerDeviceProperties) EffectiveOptimizeEnabled() bool {
	return BoolDefault(me.Optimize.Enabled, me.Optimize.EnabledByDefault)
}

// Module contains the properties and members used by all java module types
type Module struct {
	android.ModuleBase
	android.DefaultableModuleBase
	android.ApexModuleBase
	android.SdkBase

	properties       CompilerProperties
	protoProperties  android.ProtoProperties
	deviceProperties CompilerDeviceProperties

	// jar file containing header classes including static library dependencies, suitable for
	// inserting into the bootclasspath/classpath of another compile
	headerJarFile android.Path

	// jar file containing implementation classes including static library dependencies but no
	// resources
	implementationJarFile android.Path

	// jar file containing only resources including from static library dependencies
	resourceJar android.Path

	// args and dependencies to package source files into a srcjar
	srcJarArgs []string
	srcJarDeps android.Paths

	// jar file containing implementation classes and resources including static library
	// dependencies
	implementationAndResourcesJar android.Path

	// output file containing classes.dex and resources
	dexJarFile android.Path

	// output file that contains classes.dex if it should be in the output file
	maybeStrippedDexJarFile android.Path

	// output file containing uninstrumented classes that will be instrumented by jacoco
	jacocoReportClassesFile android.Path

	// output file containing mapping of obfuscated names
	proguardDictionary android.Path

	// output file of the module, which may be a classes jar or a dex jar
	outputFile       android.Path
	extraOutputFiles android.Paths

	exportAidlIncludeDirs android.Paths

	logtagsSrcs android.Paths

	// installed file for binary dependency
	installFile android.Path

	// list of .java files and srcjars that was passed to javac
	compiledJavaSrcs android.Paths
	compiledSrcJars  android.Paths

	// list of extra progurad flag files
	extraProguardFlagFiles android.Paths

	// manifest file to use instead of properties.Manifest
	overrideManifest android.OptionalPath

	// list of SDK lib names that this java module is exporting
	exportedSdkLibs []string

	// list of plugins that this java module is exporting
	exportedPluginJars android.Paths

	// list of plugins that this java module is exporting
	exportedPluginClasses []string

	// list of source files, collected from srcFiles with unique java and all kt files,
	// will be used by android.IDEInfo struct
	expandIDEInfoCompiledSrcs []string

	// expanded Jarjar_rules
	expandJarjarRules android.Path

	// list of additional targets for checkbuild
	additionalCheckedModules android.Paths

	// Extra files generated by the module type to be added as java resources.
	extraResources android.Paths

	hiddenAPI
	dexpreopter

	// list of the xref extraction files
	kytheFiles android.Paths
}

func (j *Module) OutputFiles(tag string) (android.Paths, error) {
	switch tag {
	case "":
		return append(android.Paths{j.outputFile}, j.extraOutputFiles...), nil
	case ".jar":
		return android.Paths{j.implementationAndResourcesJar}, nil
	case ".proguard_map":
		return android.Paths{j.proguardDictionary}, nil
	default:
		return nil, fmt.Errorf("unsupported module reference tag %q", tag)
	}
}

var _ android.OutputFileProducer = (*Module)(nil)

type Dependency interface {
	HeaderJars() android.Paths
	ImplementationJars() android.Paths
	ResourceJars() android.Paths
	ImplementationAndResourcesJars() android.Paths
	DexJar() android.Path
	AidlIncludeDirs() android.Paths
	ExportedSdkLibs() []string
	ExportedPlugins() (android.Paths, []string)
	SrcJarArgs() ([]string, android.Paths)
	BaseModuleName() string
	JacocoReportClassesFile() android.Path
}

type SdkLibraryDependency interface {
	SdkHeaderJars(ctx android.BaseModuleContext, sdkVersion sdkSpec) android.Paths
	SdkImplementationJars(ctx android.BaseModuleContext, sdkVersion sdkSpec) android.Paths
}

type xref interface {
	XrefJavaFiles() android.Paths
}

func (j *Module) XrefJavaFiles() android.Paths {
	return j.kytheFiles
}

func InitJavaModule(module android.DefaultableModule, hod android.HostOrDeviceSupported) {
	android.InitAndroidArchModule(module, hod, android.MultilibCommon)
	android.InitDefaultableModule(module)
}

type dependencyTag struct {
	blueprint.BaseDependencyTag
	name string
}

type jniDependencyTag struct {
	blueprint.BaseDependencyTag
}

func IsJniDepTag(depTag blueprint.DependencyTag) bool {
	_, ok := depTag.(*jniDependencyTag)
	return ok
}

var (
	staticLibTag          = dependencyTag{name: "staticlib"}
	libTag                = dependencyTag{name: "javalib"}
	java9LibTag           = dependencyTag{name: "java9lib"}
	pluginTag             = dependencyTag{name: "plugin"}
	exportedPluginTag     = dependencyTag{name: "exported-plugin"}
	bootClasspathTag      = dependencyTag{name: "bootclasspath"}
	systemModulesTag      = dependencyTag{name: "system modules"}
	frameworkResTag       = dependencyTag{name: "framework-res"}
	frameworkApkTag       = dependencyTag{name: "framework-apk"}
	kotlinStdlibTag       = dependencyTag{name: "kotlin-stdlib"}
	kotlinAnnotationsTag  = dependencyTag{name: "kotlin-annotations"}
	proguardRaiseTag      = dependencyTag{name: "proguard-raise"}
	certificateTag        = dependencyTag{name: "certificate"}
	instrumentationForTag = dependencyTag{name: "instrumentation_for"}
	usesLibTag            = dependencyTag{name: "uses-library"}
)

func IsLibDepTag(depTag blueprint.DependencyTag) bool {
	return depTag == libTag
}

func IsStaticLibDepTag(depTag blueprint.DependencyTag) bool {
	return depTag == staticLibTag
}

type sdkDep struct {
	useModule, useFiles, useDefaultLibs, invalidVersion bool

	// The modules that will be added to the bootclasspath when targeting 1.8 or lower
	bootclasspath []string

	// The default system modules to use. Will be an empty string if no system
	// modules are to be used.
	systemModules string

	// The modules that will be added ot the classpath when targeting 1.9 or higher
	java9Classpath []string

	frameworkResModule string

	jars android.Paths
	aidl android.OptionalPath

	noStandardLibs, noFrameworksLibs bool
}

func (s sdkDep) hasStandardLibs() bool {
	return !s.noStandardLibs
}

func (s sdkDep) hasFrameworkLibs() bool {
	return !s.noStandardLibs && !s.noFrameworksLibs
}

type jniLib struct {
	name   string
	path   android.Path
	target android.Target
}

func (j *Module) shouldInstrument(ctx android.BaseModuleContext) bool {
	return j.properties.Instrument && ctx.Config().IsEnvTrue("EMMA_INSTRUMENT")
}

func (j *Module) shouldInstrumentStatic(ctx android.BaseModuleContext) bool {
	return j.shouldInstrument(ctx) &&
		(ctx.Config().IsEnvTrue("EMMA_INSTRUMENT_STATIC") ||
			ctx.Config().UnbundledBuild())
}

func (j *Module) sdkVersion() sdkSpec {
	return sdkSpecFrom(String(j.deviceProperties.Sdk_version))
}

func (j *Module) systemModules() string {
	return proptools.String(j.deviceProperties.System_modules)
}

func (j *Module) minSdkVersion() sdkSpec {
	if j.deviceProperties.Min_sdk_version != nil {
		return sdkSpecFrom(*j.deviceProperties.Min_sdk_version)
	}
	return j.sdkVersion()
}

func (j *Module) targetSdkVersion() sdkSpec {
	if j.deviceProperties.Target_sdk_version != nil {
		return sdkSpecFrom(*j.deviceProperties.Target_sdk_version)
	}
	return j.sdkVersion()
}

func (j *Module) AvailableFor(what string) bool {
	if what == android.AvailableToPlatform && Bool(j.deviceProperties.Hostdex) {
		// Exception: for hostdex: true libraries, the platform variant is created
		// even if it's not marked as available to platform. In that case, the platform
		// variant is used only for the hostdex and not installed to the device.
		return true
	}
	return j.ApexModuleBase.AvailableFor(what)
}

func (j *Module) deps(ctx android.BottomUpMutatorContext) {
	if ctx.Device() {
		sdkDep := decodeSdkDep(ctx, sdkContext(j))
		if sdkDep.useDefaultLibs {
			ctx.AddVariationDependencies(nil, bootClasspathTag, config.DefaultBootclasspathLibraries...)
			ctx.AddVariationDependencies(nil, systemModulesTag, config.DefaultSystemModules)
			if sdkDep.hasFrameworkLibs() {
				ctx.AddVariationDependencies(nil, libTag, config.DefaultLibraries...)
			}
		} else if sdkDep.useModule {
			ctx.AddVariationDependencies(nil, bootClasspathTag, sdkDep.bootclasspath...)
			ctx.AddVariationDependencies(nil, systemModulesTag, sdkDep.systemModules)
			ctx.AddVariationDependencies(nil, java9LibTag, sdkDep.java9Classpath...)
			if j.deviceProperties.EffectiveOptimizeEnabled() && sdkDep.hasStandardLibs() {
				ctx.AddVariationDependencies(nil, proguardRaiseTag, config.DefaultBootclasspathLibraries...)
				ctx.AddVariationDependencies(nil, proguardRaiseTag, config.DefaultLibraries...)
			}
		}

		if ctx.ModuleName() == "android_stubs_current" ||
			ctx.ModuleName() == "android_system_stubs_current" ||
			ctx.ModuleName() == "android_test_stubs_current" {
			ctx.AddVariationDependencies(nil, frameworkApkTag, "framework-res")
		}
	}

	syspropPublicStubs := syspropPublicStubs(ctx.Config())

	// rewriteSyspropLibs validates if a java module can link against platform's sysprop_library,
	// and redirects dependency to public stub depending on the link type.
	rewriteSyspropLibs := func(libs []string, prop string) []string {
		// make a copy
		ret := android.CopyOf(libs)

		for idx, lib := range libs {
			stub, ok := syspropPublicStubs[lib]

			if !ok {
				continue
			}

			linkType, _ := j.getLinkType(ctx.ModuleName())
			// only platform modules can use internal props
			if linkType != javaPlatform {
				ret[idx] = stub
			}
		}

		return ret
	}

	ctx.AddVariationDependencies(nil, libTag, rewriteSyspropLibs(j.properties.Libs, "libs")...)
	ctx.AddVariationDependencies(nil, staticLibTag, rewriteSyspropLibs(j.properties.Static_libs, "static_libs")...)

	ctx.AddFarVariationDependencies(ctx.Config().BuildOSCommonTarget.Variations(), pluginTag, j.properties.Plugins...)
	ctx.AddFarVariationDependencies(ctx.Config().BuildOSCommonTarget.Variations(), exportedPluginTag, j.properties.Exported_plugins...)

	android.ProtoDeps(ctx, &j.protoProperties)
	if j.hasSrcExt(".proto") {
		protoDeps(ctx, &j.protoProperties)
	}

	if j.hasSrcExt(".kt") {
		// TODO(ccross): move this to a mutator pass that can tell if generated sources contain
		// Kotlin files
		ctx.AddVariationDependencies(nil, kotlinStdlibTag,
			"kotlin-stdlib", "kotlin-stdlib-jdk7", "kotlin-stdlib-jdk8")
		if len(j.properties.Plugins) > 0 {
			ctx.AddVariationDependencies(nil, kotlinAnnotationsTag, "kotlin-annotations")
		}
	}

	// Framework libraries need special handling in static coverage builds: they should not have
	// static dependency on jacoco, otherwise there would be multiple conflicting definitions of
	// the same jacoco classes coming from different bootclasspath jars.
	if inList(ctx.ModuleName(), config.InstrumentFrameworkModules) {
		if ctx.Config().IsEnvTrue("EMMA_INSTRUMENT_FRAMEWORK") {
			j.properties.Instrument = true
		}
	} else if j.shouldInstrumentStatic(ctx) {
		ctx.AddVariationDependencies(nil, staticLibTag, "jacocoagent")
	}

	// services depend on com.android.location.provider, but dependency in not registered in a Blueprint file
	if ctx.ModuleName() == "services" {
		ctx.AddDependency(ctx.Module(), dexpreopt.SystemServerForcedDepTag, "com.android.location.provider")
	}
}

func hasSrcExt(srcs []string, ext string) bool {
	for _, src := range srcs {
		if filepath.Ext(src) == ext {
			return true
		}
	}

	return false
}

func (j *Module) hasSrcExt(ext string) bool {
	return hasSrcExt(j.properties.Srcs, ext)
}

func (j *Module) aidlFlags(ctx android.ModuleContext, aidlPreprocess android.OptionalPath,
	aidlIncludeDirs android.Paths) (string, android.Paths) {

	aidlIncludes := android.PathsForModuleSrc(ctx, j.deviceProperties.Aidl.Local_include_dirs)
	aidlIncludes = append(aidlIncludes,
		android.PathsForModuleSrc(ctx, j.deviceProperties.Aidl.Export_include_dirs)...)
	aidlIncludes = append(aidlIncludes,
		android.PathsForSource(ctx, j.deviceProperties.Aidl.Include_dirs)...)

	var flags []string
	var deps android.Paths

	if aidlPreprocess.Valid() {
		flags = append(flags, "-p"+aidlPreprocess.String())
		deps = append(deps, aidlPreprocess.Path())
	} else if len(aidlIncludeDirs) > 0 {
		flags = append(flags, android.JoinWithPrefix(aidlIncludeDirs.Strings(), "-I"))
	}

	if len(j.exportAidlIncludeDirs) > 0 {
		flags = append(flags, android.JoinWithPrefix(j.exportAidlIncludeDirs.Strings(), "-I"))
	}

	if len(aidlIncludes) > 0 {
		flags = append(flags, android.JoinWithPrefix(aidlIncludes.Strings(), "-I"))
	}

	flags = append(flags, "-I"+android.PathForModuleSrc(ctx).String())
	if src := android.ExistentPathForSource(ctx, ctx.ModuleDir(), "src"); src.Valid() {
		flags = append(flags, "-I"+src.String())
	}

	if Bool(j.deviceProperties.Aidl.Generate_traces) {
		flags = append(flags, "-t")
	}

	if Bool(j.deviceProperties.Aidl.Generate_get_transaction_name) {
		flags = append(flags, "--transaction_names")
	}

	return strings.Join(flags, " "), deps
}

type deps struct {
	classpath          classpath
	java9Classpath     classpath
	bootClasspath      classpath
	processorPath      classpath
	processorClasses   []string
	staticJars         android.Paths
	staticHeaderJars   android.Paths
	staticResourceJars android.Paths
	aidlIncludeDirs    android.Paths
	srcs               android.Paths
	srcJars            android.Paths
	systemModules      *systemModules
	aidlPreprocess     android.OptionalPath
	kotlinStdlib       android.Paths
	kotlinAnnotations  android.Paths

	disableTurbine bool
}

func checkProducesJars(ctx android.ModuleContext, dep android.SourceFileProducer) {
	for _, f := range dep.Srcs() {
		if f.Ext() != ".jar" {
			ctx.ModuleErrorf("genrule %q must generate files ending with .jar to be used as a libs or static_libs dependency",
				ctx.OtherModuleName(dep.(blueprint.Module)))
		}
	}
}

type linkType int

const (
	// TODO(jiyong) rename these for better readability. Make the allowed
	// and disallowed link types explicit
	javaCore linkType = iota
	javaSdk
	javaSystem
	javaModule
	javaSystemServer
	javaPlatform
)

type linkTypeContext interface {
	android.Module
	getLinkType(name string) (ret linkType, stubs bool)
}

func (m *Module) getLinkType(name string) (ret linkType, stubs bool) {
	ver := m.sdkVersion()
	switch {
	case name == "core.current.stubs" || name == "core.platform.api.stubs" ||
		name == "stub-annotations" || name == "private-stub-annotations-jar" ||
		name == "core-lambda-stubs" || name == "core-generated-annotation-stubs":
		return javaCore, true
	case ver.kind == sdkCore:
		return javaCore, false
	case name == "android_system_stubs_current":
		return javaSystem, true
	case ver.kind == sdkSystem:
		return javaSystem, false
	case name == "android_test_stubs_current":
		return javaSystem, true
	case ver.kind == sdkTest:
		return javaPlatform, false
	case name == "android_stubs_current":
		return javaSdk, true
	case ver.kind == sdkPublic:
		return javaSdk, false
	case name == "android_module_lib_stubs_current":
		return javaModule, true
	case ver.kind == sdkModule:
		return javaModule, false
	case name == "services-stubs":
		return javaSystemServer, true
	case ver.kind == sdkSystemServer:
		return javaSystemServer, false
	case ver.kind == sdkPrivate || ver.kind == sdkNone || ver.kind == sdkCorePlatform:
		return javaPlatform, false
	case !ver.valid():
		panic(fmt.Errorf("sdk_version is invalid. got %q", ver.raw))
	default:
		return javaSdk, false
	}
}

func checkLinkType(ctx android.ModuleContext, from *Module, to linkTypeContext, tag dependencyTag) {
	if ctx.Host() {
		return
	}

	myLinkType, stubs := from.getLinkType(ctx.ModuleName())
	if stubs {
		return
	}
	otherLinkType, _ := to.getLinkType(ctx.OtherModuleName(to))
	commonMessage := "Adjust sdk_version: property of the source or target module so that target module is built with the same or smaller API set than the source."

	switch myLinkType {
	case javaCore:
		if otherLinkType != javaCore {
			ctx.ModuleErrorf("compiles against core Java API, but dependency %q is compiling against non-core Java APIs."+commonMessage,
				ctx.OtherModuleName(to))
		}
		break
	case javaSdk:
		if otherLinkType != javaCore && otherLinkType != javaSdk {
			ctx.ModuleErrorf("compiles against Android API, but dependency %q is compiling against non-public Android API."+commonMessage,
				ctx.OtherModuleName(to))
		}
		break
	case javaSystem:
		if otherLinkType == javaPlatform || otherLinkType == javaModule || otherLinkType == javaSystemServer {
			ctx.ModuleErrorf("compiles against system API, but dependency %q is compiling against private API."+commonMessage,
				ctx.OtherModuleName(to))
		}
		break
	case javaModule:
		if otherLinkType == javaPlatform || otherLinkType == javaSystemServer {
			ctx.ModuleErrorf("compiles against module API, but dependency %q is compiling against private API."+commonMessage,
				ctx.OtherModuleName(to))
		}
		break
	case javaSystemServer:
		if otherLinkType == javaPlatform {
			ctx.ModuleErrorf("compiles against system server API, but dependency %q is compiling against private API."+commonMessage,
				ctx.OtherModuleName(to))
		}
		break
	case javaPlatform:
		// no restriction on link-type
		break
	}
}

func (j *Module) collectDeps(ctx android.ModuleContext) deps {
	var deps deps

	if ctx.Device() {
		sdkDep := decodeSdkDep(ctx, sdkContext(j))
		if sdkDep.invalidVersion {
			ctx.AddMissingDependencies(sdkDep.bootclasspath)
			ctx.AddMissingDependencies(sdkDep.java9Classpath)
		} else if sdkDep.useFiles {
			// sdkDep.jar is actually equivalent to turbine header.jar.
			deps.classpath = append(deps.classpath, sdkDep.jars...)
			deps.aidlPreprocess = sdkDep.aidl
		} else {
			deps.aidlPreprocess = sdkDep.aidl
		}
	}

	ctx.VisitDirectDeps(func(module android.Module) {
		otherName := ctx.OtherModuleName(module)
		tag := ctx.OtherModuleDependencyTag(module)

		if _, ok := tag.(*jniDependencyTag); ok {
			// Handled by AndroidApp.collectAppDeps
			return
		}
		if tag == certificateTag {
			// Handled by AndroidApp.collectAppDeps
			return
		}
		switch module.(type) {
		case *Library, *AndroidLibrary:
			if to, ok := module.(linkTypeContext); ok {
				switch tag {
				case bootClasspathTag, libTag, staticLibTag:
					checkLinkType(ctx, j, to, tag.(dependencyTag))
				}
			}
		}
		switch dep := module.(type) {
		case SdkLibraryDependency:
			switch tag {
			case libTag:
				deps.classpath = append(deps.classpath, dep.SdkHeaderJars(ctx, j.sdkVersion())...)
				// names of sdk libs that are directly depended are exported
				j.exportedSdkLibs = append(j.exportedSdkLibs, otherName)
			case staticLibTag:
				ctx.ModuleErrorf("dependency on java_sdk_library %q can only be in libs", otherName)
			}
		case Dependency:
			switch tag {
			case bootClasspathTag:
				deps.bootClasspath = append(deps.bootClasspath, dep.HeaderJars()...)
			case libTag, instrumentationForTag:
				deps.classpath = append(deps.classpath, dep.HeaderJars()...)
				// sdk lib names from dependencies are re-exported
				j.exportedSdkLibs = append(j.exportedSdkLibs, dep.ExportedSdkLibs()...)
				deps.aidlIncludeDirs = append(deps.aidlIncludeDirs, dep.AidlIncludeDirs()...)
				pluginJars, pluginClasses := dep.ExportedPlugins()
				addPlugins(&deps, pluginJars, pluginClasses...)
			case java9LibTag:
				deps.java9Classpath = append(deps.java9Classpath, dep.HeaderJars()...)
			case staticLibTag:
				deps.classpath = append(deps.classpath, dep.HeaderJars()...)
				deps.staticJars = append(deps.staticJars, dep.ImplementationJars()...)
				deps.staticHeaderJars = append(deps.staticHeaderJars, dep.HeaderJars()...)
				deps.staticResourceJars = append(deps.staticResourceJars, dep.ResourceJars()...)
				// sdk lib names from dependencies are re-exported
				j.exportedSdkLibs = append(j.exportedSdkLibs, dep.ExportedSdkLibs()...)
				deps.aidlIncludeDirs = append(deps.aidlIncludeDirs, dep.AidlIncludeDirs()...)
				pluginJars, pluginClasses := dep.ExportedPlugins()
				addPlugins(&deps, pluginJars, pluginClasses...)
			case pluginTag:
				if plugin, ok := dep.(*Plugin); ok {
					if plugin.pluginProperties.Processor_class != nil {
						addPlugins(&deps, plugin.ImplementationAndResourcesJars(), *plugin.pluginProperties.Processor_class)
					} else {
						addPlugins(&deps, plugin.ImplementationAndResourcesJars())
					}
					deps.disableTurbine = deps.disableTurbine || Bool(plugin.pluginProperties.Generates_api)
				} else {
					ctx.PropertyErrorf("plugins", "%q is not a java_plugin module", otherName)
				}
			case exportedPluginTag:
				if plugin, ok := dep.(*Plugin); ok {
					if plugin.pluginProperties.Generates_api != nil && *plugin.pluginProperties.Generates_api {
						ctx.PropertyErrorf("exported_plugins", "Cannot export plugins with generates_api = true, found %v", otherName)
					}
					j.exportedPluginJars = append(j.exportedPluginJars, plugin.ImplementationAndResourcesJars()...)
					if plugin.pluginProperties.Processor_class != nil {
						j.exportedPluginClasses = append(j.exportedPluginClasses, *plugin.pluginProperties.Processor_class)
					}
				} else {
					ctx.PropertyErrorf("exported_plugins", "%q is not a java_plugin module", otherName)
				}
			case frameworkApkTag:
				if ctx.ModuleName() == "android_stubs_current" ||
					ctx.ModuleName() == "android_system_stubs_current" ||
					ctx.ModuleName() == "android_test_stubs_current" {
					// framework stubs.jar need to depend on framework-res.apk, in order to pull the
					// resource files out of there for aapt.
					//
					// Normally the package rule runs aapt, which includes the resource,
					// but we're not running that in our package rule so just copy in the
					// resource files here.
					deps.staticResourceJars = append(deps.staticResourceJars, dep.(*AndroidApp).exportPackage)
				}
			case kotlinStdlibTag:
				deps.kotlinStdlib = append(deps.kotlinStdlib, dep.HeaderJars()...)
			case kotlinAnnotationsTag:
				deps.kotlinAnnotations = dep.HeaderJars()
			}

		case android.SourceFileProducer:
			switch tag {
			case libTag:
				checkProducesJars(ctx, dep)
				deps.classpath = append(deps.classpath, dep.Srcs()...)
			case staticLibTag:
				checkProducesJars(ctx, dep)
				deps.classpath = append(deps.classpath, dep.Srcs()...)
				deps.staticJars = append(deps.staticJars, dep.Srcs()...)
				deps.staticHeaderJars = append(deps.staticHeaderJars, dep.Srcs()...)
			}
		default:
			switch tag {
			case bootClasspathTag:
				// If a system modules dependency has been added to the bootclasspath
				// then add its libs to the bootclasspath.
				sm := module.(SystemModulesProvider)
				deps.bootClasspath = append(deps.bootClasspath, sm.HeaderJars()...)

			case systemModulesTag:
				if deps.systemModules != nil {
					panic("Found two system module dependencies")
				}
				sm := module.(SystemModulesProvider)
				outputDir, outputDeps := sm.OutputDirAndDeps()
				deps.systemModules = &systemModules{outputDir, outputDeps}
			}
		}
	})

	j.exportedSdkLibs = android.FirstUniqueStrings(j.exportedSdkLibs)

	return deps
}

func addPlugins(deps *deps, pluginJars android.Paths, pluginClasses ...string) {
	deps.processorPath = append(deps.processorPath, pluginJars...)
	deps.processorClasses = append(deps.processorClasses, pluginClasses...)
}

func getJavaVersion(ctx android.ModuleContext, javaVersion string, sdkContext sdkContext) javaVersion {
	sdk, err := sdkContext.sdkVersion().effectiveVersion(ctx)
	if err != nil {
		ctx.PropertyErrorf("sdk_version", "%s", err)
	}
	if javaVersion != "" {
		return normalizeJavaVersion(ctx, javaVersion)
	} else if ctx.Device() && sdk <= 23 {
		return JAVA_VERSION_7
	} else if ctx.Device() && sdk <= 29 {
		return JAVA_VERSION_8
	} else if ctx.Device() && ctx.Config().UnbundledBuildUsePrebuiltSdks() {
		// TODO(b/142896162): once we have prebuilt system modules we can use 1.9 for unbundled builds
		return JAVA_VERSION_8
	} else {
		return JAVA_VERSION_9
	}
}

type javaVersion int

const (
	JAVA_VERSION_UNSUPPORTED = 0
	JAVA_VERSION_6           = 6
	JAVA_VERSION_7           = 7
	JAVA_VERSION_8           = 8
	JAVA_VERSION_9           = 9
)

func (v javaVersion) String() string {
	switch v {
	case JAVA_VERSION_6:
		return "1.6"
	case JAVA_VERSION_7:
		return "1.7"
	case JAVA_VERSION_8:
		return "1.8"
	case JAVA_VERSION_9:
		return "1.9"
	default:
		return "unsupported"
	}
}

// Returns true if javac targeting this version uses system modules instead of a bootclasspath.
func (v javaVersion) usesJavaModules() bool {
	return v >= 9
}

func normalizeJavaVersion(ctx android.BaseModuleContext, javaVersion string) javaVersion {
	switch javaVersion {
	case "1.6", "6":
		return JAVA_VERSION_6
	case "1.7", "7":
		return JAVA_VERSION_7
	case "1.8", "8":
		return JAVA_VERSION_8
	case "1.9", "9":
		return JAVA_VERSION_9
	case "10", "11":
		ctx.PropertyErrorf("java_version", "Java language levels above 9 are not supported")
		return JAVA_VERSION_UNSUPPORTED
	default:
		ctx.PropertyErrorf("java_version", "Unrecognized Java language level")
		return JAVA_VERSION_UNSUPPORTED
	}
}

func (j *Module) collectBuilderFlags(ctx android.ModuleContext, deps deps) javaBuilderFlags {

	var flags javaBuilderFlags

	// javaVersion flag.
	flags.javaVersion = getJavaVersion(ctx, String(j.properties.Java_version), sdkContext(j))

	// javac flags.
	javacFlags := j.properties.Javacflags
	if flags.javaVersion.usesJavaModules() {
		javacFlags = append(javacFlags, j.properties.Openjdk9.Javacflags...)
	}
	if ctx.Config().MinimizeJavaDebugInfo() {
		// Override the -g flag passed globally to remove local variable debug info to reduce
		// disk and memory usage.
		javacFlags = append(javacFlags, "-g:source,lines")
	}
	javacFlags = append(javacFlags, "-Xlint:-dep-ann")

	if ctx.Config().RunErrorProne() {
		if config.ErrorProneClasspath == nil {
			ctx.ModuleErrorf("cannot build with Error Prone, missing external/error_prone?")
		}

		errorProneFlags := []string{
			"-Xplugin:ErrorProne",
			"${config.ErrorProneChecks}",
		}
		errorProneFlags = append(errorProneFlags, j.properties.Errorprone.Javacflags...)

		flags.errorProneExtraJavacFlags = "${config.ErrorProneFlags} " +
			"'" + strings.Join(errorProneFlags, " ") + "'"
		flags.errorProneProcessorPath = classpath(android.PathsForSource(ctx, config.ErrorProneClasspath))
	}

	// classpath
	flags.bootClasspath = append(flags.bootClasspath, deps.bootClasspath...)
	flags.classpath = append(flags.classpath, deps.classpath...)
	flags.java9Classpath = append(flags.java9Classpath, deps.java9Classpath...)
	flags.processorPath = append(flags.processorPath, deps.processorPath...)

	flags.processor = strings.Join(deps.processorClasses, ",")

	if len(flags.bootClasspath) == 0 && ctx.Host() && !flags.javaVersion.usesJavaModules() &&
		decodeSdkDep(ctx, sdkContext(j)).hasStandardLibs() {
		// Give host-side tools a version of OpenJDK's standard libraries
		// close to what they're targeting. As of Dec 2017, AOSP is only
		// bundling OpenJDK 8 and 9, so nothing < 8 is available.
		//
		// When building with OpenJDK 8, the following should have no
		// effect since those jars would be available by default.
		//
		// When building with OpenJDK 9 but targeting a version < 1.8,
		// putting them on the bootclasspath means that:
		// a) code can't (accidentally) refer to OpenJDK 9 specific APIs
		// b) references to existing APIs are not reinterpreted in an
		//    OpenJDK 9-specific way, eg. calls to subclasses of
		//    java.nio.Buffer as in http://b/70862583
		java8Home := ctx.Config().Getenv("ANDROID_JAVA8_HOME")
		flags.bootClasspath = append(flags.bootClasspath,
			android.PathForSource(ctx, java8Home, "jre/lib/jce.jar"),
			android.PathForSource(ctx, java8Home, "jre/lib/rt.jar"))
		if Bool(j.properties.Use_tools_jar) {
			flags.bootClasspath = append(flags.bootClasspath,
				android.PathForSource(ctx, java8Home, "lib/tools.jar"))
		}
	}

	if j.properties.Patch_module != nil && flags.javaVersion.usesJavaModules() {
		// Manually specify build directory in case it is not under the repo root.
		// (javac doesn't seem to expand into symbolc links when searching for patch-module targets, so
		// just adding a symlink under the root doesn't help.)
		patchPaths := ".:" + ctx.Config().BuildDir()
		classPath := flags.classpath.FormJavaClassPath("")
		if classPath != "" {
			patchPaths += ":" + classPath
		}
		javacFlags = append(javacFlags, "--patch-module="+String(j.properties.Patch_module)+"="+patchPaths)
	}

	// systemModules
	flags.systemModules = deps.systemModules

	// aidl flags.
	flags.aidlFlags, flags.aidlDeps = j.aidlFlags(ctx, deps.aidlPreprocess, deps.aidlIncludeDirs)

	if len(javacFlags) > 0 {
		// optimization.
		ctx.Variable(pctx, "javacFlags", strings.Join(javacFlags, " "))
		flags.javacFlags = "$javacFlags"
	}

	return flags
}

func (j *Module) compile(ctx android.ModuleContext, aaptSrcJar android.Path) {
	j.exportAidlIncludeDirs = android.PathsForModuleSrc(ctx, j.deviceProperties.Aidl.Export_include_dirs)

	deps := j.collectDeps(ctx)
	flags := j.collectBuilderFlags(ctx, deps)

	if flags.javaVersion.usesJavaModules() {
		j.properties.Srcs = append(j.properties.Srcs, j.properties.Openjdk9.Srcs...)
	}
	srcFiles := android.PathsForModuleSrcExcludes(ctx, j.properties.Srcs, j.properties.Exclude_srcs)
	if hasSrcExt(srcFiles.Strings(), ".proto") {
		flags = protoFlags(ctx, &j.properties, &j.protoProperties, flags)
	}

	srcFiles = j.genSources(ctx, srcFiles, flags)

	srcJars := srcFiles.FilterByExt(".srcjar")
	srcJars = append(srcJars, deps.srcJars...)
	if aaptSrcJar != nil {
		srcJars = append(srcJars, aaptSrcJar)
	}

	if j.properties.Jarjar_rules != nil {
		j.expandJarjarRules = android.PathForModuleSrc(ctx, *j.properties.Jarjar_rules)
	}

	jarName := ctx.ModuleName() + ".jar"

	javaSrcFiles := srcFiles.FilterByExt(".java")
	var uniqueSrcFiles android.Paths
	set := make(map[string]bool)
	for _, v := range javaSrcFiles {
		if _, found := set[v.String()]; !found {
			set[v.String()] = true
			uniqueSrcFiles = append(uniqueSrcFiles, v)
		}
	}

	// Collect .java files for AIDEGen
	j.expandIDEInfoCompiledSrcs = append(j.expandIDEInfoCompiledSrcs, uniqueSrcFiles.Strings()...)

	var kotlinJars android.Paths

	if srcFiles.HasExt(".kt") {
		// user defined kotlin flags.
		kotlincFlags := j.properties.Kotlincflags
		CheckKotlincFlags(ctx, kotlincFlags)

		// If there are kotlin files, compile them first but pass all the kotlin and java files
		// kotlinc will use the java files to resolve types referenced by the kotlin files, but
		// won't emit any classes for them.
		kotlincFlags = append(kotlincFlags, "-no-stdlib")
		if ctx.Device() {
			kotlincFlags = append(kotlincFlags, "-no-jdk")
		}
		if len(kotlincFlags) > 0 {
			// optimization.
			ctx.Variable(pctx, "kotlincFlags", strings.Join(kotlincFlags, " "))
			flags.kotlincFlags += "$kotlincFlags"
		}

		var kotlinSrcFiles android.Paths
		kotlinSrcFiles = append(kotlinSrcFiles, uniqueSrcFiles...)
		kotlinSrcFiles = append(kotlinSrcFiles, srcFiles.FilterByExt(".kt")...)

		// Collect .kt files for AIDEGen
		j.expandIDEInfoCompiledSrcs = append(j.expandIDEInfoCompiledSrcs, srcFiles.FilterByExt(".kt").Strings()...)

		flags.classpath = append(flags.classpath, deps.kotlinStdlib...)
		flags.classpath = append(flags.classpath, deps.kotlinAnnotations...)

		flags.kotlincClasspath = append(flags.kotlincClasspath, flags.bootClasspath...)
		flags.kotlincClasspath = append(flags.kotlincClasspath, flags.classpath...)

		if len(flags.processorPath) > 0 {
			// Use kapt for annotation processing
			kaptSrcJar := android.PathForModuleOut(ctx, "kapt", "kapt-sources.jar")
			kotlinKapt(ctx, kaptSrcJar, kotlinSrcFiles, srcJars, flags)
			srcJars = append(srcJars, kaptSrcJar)
			// Disable annotation processing in javac, it's already been handled by kapt
			flags.processorPath = nil
			flags.processor = ""
		}

		kotlinJar := android.PathForModuleOut(ctx, "kotlin", jarName)
		kotlinCompile(ctx, kotlinJar, kotlinSrcFiles, srcJars, flags)
		if ctx.Failed() {
			return
		}

		// Make javac rule depend on the kotlinc rule
		flags.classpath = append(flags.classpath, kotlinJar)

		// Jar kotlin classes into the final jar after javac
		kotlinJars = append(kotlinJars, kotlinJar)
		kotlinJars = append(kotlinJars, deps.kotlinStdlib...)
	}

	jars := append(android.Paths(nil), kotlinJars...)

	// Store the list of .java files that was passed to javac
	j.compiledJavaSrcs = uniqueSrcFiles
	j.compiledSrcJars = srcJars

	enable_sharding := false
	var headerJarFileWithoutJarjar android.Path
	if ctx.Device() && !ctx.Config().IsEnvFalse("TURBINE_ENABLED") && !deps.disableTurbine {
		if j.properties.Javac_shard_size != nil && *(j.properties.Javac_shard_size) > 0 {
			enable_sharding = true
			// Formerly, there was a check here that prevented annotation processors
			// from being used when sharding was enabled, as some annotation processors
			// do not function correctly in sharded environments. It was removed to
			// allow for the use of annotation processors that do function correctly
			// with sharding enabled. See: b/77284273.
		}
		headerJarFileWithoutJarjar, j.headerJarFile =
			j.compileJavaHeader(ctx, uniqueSrcFiles, srcJars, deps, flags, jarName, kotlinJars)
		if ctx.Failed() {
			return
		}
	}
	if len(uniqueSrcFiles) > 0 || len(srcJars) > 0 {
		var extraJarDeps android.Paths
		if ctx.Config().RunErrorProne() {
			// If error-prone is enabled, add an additional rule to compile the java files into
			// a separate set of classes (so that they don't overwrite the normal ones and require
			// a rebuild when error-prone is turned off).
			// TODO(ccross): Once we always compile with javac9 we may be able to conditionally
			//    enable error-prone without affecting the output class files.
			errorprone := android.PathForModuleOut(ctx, "errorprone", jarName)
			RunErrorProne(ctx, errorprone, uniqueSrcFiles, srcJars, flags)
			extraJarDeps = append(extraJarDeps, errorprone)
		}

		if enable_sharding {
			flags.classpath = append(flags.classpath, headerJarFileWithoutJarjar)
			shardSize := int(*(j.properties.Javac_shard_size))
			var shardSrcs []android.Paths
			if len(uniqueSrcFiles) > 0 {
				shardSrcs = android.ShardPaths(uniqueSrcFiles, shardSize)
				for idx, shardSrc := range shardSrcs {
					classes := j.compileJavaClasses(ctx, jarName, idx, shardSrc,
						nil, flags, extraJarDeps)
					jars = append(jars, classes)
				}
			}
			if len(srcJars) > 0 {
				classes := j.compileJavaClasses(ctx, jarName, len(shardSrcs),
					nil, srcJars, flags, extraJarDeps)
				jars = append(jars, classes)
			}
		} else {
			classes := j.compileJavaClasses(ctx, jarName, -1, uniqueSrcFiles, srcJars, flags, extraJarDeps)
			jars = append(jars, classes)
		}
		if ctx.Failed() {
			return
		}
	}

	j.srcJarArgs, j.srcJarDeps = resourcePathsToJarArgs(srcFiles), srcFiles

	var includeSrcJar android.WritablePath
	if Bool(j.properties.Include_srcs) {
		includeSrcJar = android.PathForModuleOut(ctx, ctx.ModuleName()+".srcjar")
		TransformResourcesToJar(ctx, includeSrcJar, j.srcJarArgs, j.srcJarDeps)
	}

	dirArgs, dirDeps := ResourceDirsToJarArgs(ctx, j.properties.Java_resource_dirs,
		j.properties.Exclude_java_resource_dirs, j.properties.Exclude_java_resources)
	fileArgs, fileDeps := ResourceFilesToJarArgs(ctx, j.properties.Java_resources, j.properties.Exclude_java_resources)
	extraArgs, extraDeps := resourcePathsToJarArgs(j.extraResources), j.extraResources

	var resArgs []string
	var resDeps android.Paths

	resArgs = append(resArgs, dirArgs...)
	resDeps = append(resDeps, dirDeps...)

	resArgs = append(resArgs, fileArgs...)
	resDeps = append(resDeps, fileDeps...)

	resArgs = append(resArgs, extraArgs...)
	resDeps = append(resDeps, extraDeps...)

	if len(resArgs) > 0 {
		resourceJar := android.PathForModuleOut(ctx, "res", jarName)
		TransformResourcesToJar(ctx, resourceJar, resArgs, resDeps)
		j.resourceJar = resourceJar
		if ctx.Failed() {
			return
		}
	}

	var resourceJars android.Paths
	if j.resourceJar != nil {
		resourceJars = append(resourceJars, j.resourceJar)
	}
	if Bool(j.properties.Include_srcs) {
		resourceJars = append(resourceJars, includeSrcJar)
	}
	resourceJars = append(resourceJars, deps.staticResourceJars...)

	if len(resourceJars) > 1 {
		combinedJar := android.PathForModuleOut(ctx, "res-combined", jarName)
		TransformJarsToJar(ctx, combinedJar, "for resources", resourceJars, android.OptionalPath{},
			false, nil, nil)
		j.resourceJar = combinedJar
	} else if len(resourceJars) == 1 {
		j.resourceJar = resourceJars[0]
	}

	if len(deps.staticJars) > 0 {
		jars = append(jars, deps.staticJars...)
	}

	manifest := j.overrideManifest
	if !manifest.Valid() && j.properties.Manifest != nil {
		manifest = android.OptionalPathForPath(android.PathForModuleSrc(ctx, *j.properties.Manifest))
	}

	services := android.PathsForModuleSrc(ctx, j.properties.Services)
	if len(services) > 0 {
		servicesJar := android.PathForModuleOut(ctx, "services", jarName)
		var zipargs []string
		for _, file := range services {
			serviceFile := file.String()
			zipargs = append(zipargs, "-C", filepath.Dir(serviceFile), "-f", serviceFile)
		}
		ctx.Build(pctx, android.BuildParams{
			Rule:      zip,
			Output:    servicesJar,
			Implicits: services,
			Args: map[string]string{
				"jarArgs": "-P META-INF/services/ " + strings.Join(proptools.NinjaAndShellEscapeList(zipargs), " "),
			},
		})
		jars = append(jars, servicesJar)
	}

	// Combine the classes built from sources, any manifests, and any static libraries into
	// classes.jar. If there is only one input jar this step will be skipped.
	var outputFile android.ModuleOutPath

	if len(jars) == 1 && !manifest.Valid() {
		if moduleOutPath, ok := jars[0].(android.ModuleOutPath); ok {
			// Optimization: skip the combine step if there is nothing to do
			// TODO(ccross): this leaves any module-info.class files, but those should only come from
			// prebuilt dependencies until we support modules in the platform build, so there shouldn't be
			// any if len(jars) == 1.
			outputFile = moduleOutPath
		} else {
			combinedJar := android.PathForModuleOut(ctx, "combined", jarName)
			ctx.Build(pctx, android.BuildParams{
				Rule:   android.Cp,
				Input:  jars[0],
				Output: combinedJar,
			})
			outputFile = combinedJar
		}
	} else {
		combinedJar := android.PathForModuleOut(ctx, "combined", jarName)
		TransformJarsToJar(ctx, combinedJar, "for javac", jars, manifest,
			false, nil, nil)
		outputFile = combinedJar
	}

	// jarjar implementation jar if necessary
	if j.expandJarjarRules != nil {
		// Transform classes.jar into classes-jarjar.jar
		jarjarFile := android.PathForModuleOut(ctx, "jarjar", jarName)
		TransformJarJar(ctx, jarjarFile, outputFile, j.expandJarjarRules)
		outputFile = jarjarFile

		// jarjar resource jar if necessary
		if j.resourceJar != nil {
			resourceJarJarFile := android.PathForModuleOut(ctx, "res-jarjar", jarName)
			TransformJarJar(ctx, resourceJarJarFile, j.resourceJar, j.expandJarjarRules)
			j.resourceJar = resourceJarJarFile
		}

		if ctx.Failed() {
			return
		}
	}

	// Check package restrictions if necessary.
	if len(j.properties.Permitted_packages) > 0 {
		// Check packages and copy to package-checked file.
		pkgckFile := android.PathForModuleOut(ctx, "package-check.stamp")
		CheckJarPackages(ctx, pkgckFile, outputFile, j.properties.Permitted_packages)
		j.additionalCheckedModules = append(j.additionalCheckedModules, pkgckFile)

		if ctx.Failed() {
			return
		}
	}

	j.implementationJarFile = outputFile
	if j.headerJarFile == nil {
		j.headerJarFile = j.implementationJarFile
	}

	// Force enable the instrumentation for java code that is built for APEXes ...
	// except for the jacocoagent itself (because instrumenting jacocoagent using jacocoagent
	// doesn't make sense)
	isJacocoAgent := ctx.ModuleName() == "jacocoagent"
	if android.DirectlyInAnyApex(ctx, ctx.ModuleName()) && !isJacocoAgent && !j.IsForPlatform() {
		j.properties.Instrument = true
	}

	if j.shouldInstrument(ctx) {
		outputFile = j.instrument(ctx, flags, outputFile, jarName)
	}

	// merge implementation jar with resources if necessary
	implementationAndResourcesJar := outputFile
	if j.resourceJar != nil {
		jars := android.Paths{j.resourceJar, implementationAndResourcesJar}
		combinedJar := android.PathForModuleOut(ctx, "withres", jarName)
		TransformJarsToJar(ctx, combinedJar, "for resources", jars, manifest,
			false, nil, nil)
		implementationAndResourcesJar = combinedJar
	}

	j.implementationAndResourcesJar = implementationAndResourcesJar

	// Enable dex compilation for the APEX variants, unless it is disabled explicitly
	if android.DirectlyInAnyApex(ctx, ctx.ModuleName()) && !j.IsForPlatform() {
		if j.deviceProperties.Compile_dex == nil {
			j.deviceProperties.Compile_dex = proptools.BoolPtr(true)
		}
		if j.deviceProperties.Hostdex == nil {
			j.deviceProperties.Hostdex = proptools.BoolPtr(true)
		}
	}

	if ctx.Device() && j.hasCode(ctx) &&
		(Bool(j.properties.Installable) || Bool(j.deviceProperties.Compile_dex)) {
		// Dex compilation
		var dexOutputFile android.ModuleOutPath
		dexOutputFile = j.compileDex(ctx, flags, outputFile, jarName)
		if ctx.Failed() {
			return
		}

		// Hidden API CSV generation and dex encoding
		dexOutputFile = j.hiddenAPI.hiddenAPI(ctx, dexOutputFile, j.implementationJarFile,
			j.deviceProperties.UncompressDex)

		// merge dex jar with resources if necessary
		if j.resourceJar != nil {
			jars := android.Paths{dexOutputFile, j.resourceJar}
			combinedJar := android.PathForModuleOut(ctx, "dex-withres", jarName)
			TransformJarsToJar(ctx, combinedJar, "for dex resources", jars, android.OptionalPath{},
				false, nil, nil)
			if j.deviceProperties.UncompressDex {
				combinedAlignedJar := android.PathForModuleOut(ctx, "dex-withres-aligned", jarName)
				TransformZipAlign(ctx, combinedAlignedJar, combinedJar)
				dexOutputFile = combinedAlignedJar
			} else {
				dexOutputFile = combinedJar
			}
		}

		j.dexJarFile = dexOutputFile

		// Dexpreopting
		dexOutputFile = j.dexpreopt(ctx, dexOutputFile)

		j.maybeStrippedDexJarFile = dexOutputFile

		outputFile = dexOutputFile

		if ctx.Failed() {
			return
		}
	} else {
		outputFile = implementationAndResourcesJar
	}

	ctx.CheckbuildFile(outputFile)

	// Save the output file with no relative path so that it doesn't end up in a subdirectory when used as a resource
	j.outputFile = outputFile.WithoutRel()
}

func (j *Module) compileJavaClasses(ctx android.ModuleContext, jarName string, idx int,
	srcFiles, srcJars android.Paths, flags javaBuilderFlags, extraJarDeps android.Paths) android.WritablePath {

	kzipName := pathtools.ReplaceExtension(jarName, "kzip")
	if idx >= 0 {
		kzipName = strings.TrimSuffix(jarName, filepath.Ext(jarName)) + strconv.Itoa(idx) + ".kzip"
		jarName += strconv.Itoa(idx)
	}

	classes := android.PathForModuleOut(ctx, "javac", jarName)
	TransformJavaToClasses(ctx, classes, idx, srcFiles, srcJars, flags, extraJarDeps)

	if ctx.Config().EmitXrefRules() {
		extractionFile := android.PathForModuleOut(ctx, kzipName)
		emitXrefRule(ctx, extractionFile, idx, srcFiles, srcJars, flags, extraJarDeps)
		j.kytheFiles = append(j.kytheFiles, extractionFile)
	}

	return classes
}

// Check for invalid kotlinc flags. Only use this for flags explicitly passed by the user,
// since some of these flags may be used internally.
func CheckKotlincFlags(ctx android.ModuleContext, flags []string) {
	for _, flag := range flags {
		flag = strings.TrimSpace(flag)

		if !strings.HasPrefix(flag, "-") {
			ctx.PropertyErrorf("kotlincflags", "Flag `%s` must start with `-`", flag)
		} else if strings.HasPrefix(flag, "-Xintellij-plugin-root") {
			ctx.PropertyErrorf("kotlincflags",
				"Bad flag: `%s`, only use internal compiler for consistency.", flag)
		} else if inList(flag, config.KotlincIllegalFlags) {
			ctx.PropertyErrorf("kotlincflags", "Flag `%s` already used by build system", flag)
		} else if flag == "-include-runtime" {
			ctx.PropertyErrorf("kotlincflags", "Bad flag: `%s`, do not include runtime.", flag)
		} else {
			args := strings.Split(flag, " ")
			if args[0] == "-kotlin-home" {
				ctx.PropertyErrorf("kotlincflags",
					"Bad flag: `%s`, kotlin home already set to default (path to kotlinc in the repo).", flag)
			}
		}
	}
}

func (j *Module) compileJavaHeader(ctx android.ModuleContext, srcFiles, srcJars android.Paths,
	deps deps, flags javaBuilderFlags, jarName string,
	extraJars android.Paths) (headerJar, jarjarHeaderJar android.Path) {

	var jars android.Paths
	if len(srcFiles) > 0 || len(srcJars) > 0 {
		// Compile java sources into turbine.jar.
		turbineJar := android.PathForModuleOut(ctx, "turbine", jarName)
		TransformJavaToHeaderClasses(ctx, turbineJar, srcFiles, srcJars, flags)
		if ctx.Failed() {
			return nil, nil
		}
		jars = append(jars, turbineJar)
	}

	jars = append(jars, extraJars...)

	// Combine any static header libraries into classes-header.jar. If there is only
	// one input jar this step will be skipped.
	jars = append(jars, deps.staticHeaderJars...)

	// we cannot skip the combine step for now if there is only one jar
	// since we have to strip META-INF/TRANSITIVE dir from turbine.jar
	combinedJar := android.PathForModuleOut(ctx, "turbine-combined", jarName)
	TransformJarsToJar(ctx, combinedJar, "for turbine", jars, android.OptionalPath{},
		false, nil, []string{"META-INF/TRANSITIVE"})
	headerJar = combinedJar
	jarjarHeaderJar = combinedJar

	if j.expandJarjarRules != nil {
		// Transform classes.jar into classes-jarjar.jar
		jarjarFile := android.PathForModuleOut(ctx, "turbine-jarjar", jarName)
		TransformJarJar(ctx, jarjarFile, headerJar, j.expandJarjarRules)
		jarjarHeaderJar = jarjarFile
		if ctx.Failed() {
			return nil, nil
		}
	}

	return headerJar, jarjarHeaderJar
}

func (j *Module) instrument(ctx android.ModuleContext, flags javaBuilderFlags,
	classesJar android.Path, jarName string) android.ModuleOutPath {

	specs := j.jacocoModuleToZipCommand(ctx)

	jacocoReportClassesFile := android.PathForModuleOut(ctx, "jacoco-report-classes", jarName)
	instrumentedJar := android.PathForModuleOut(ctx, "jacoco", jarName)

	jacocoInstrumentJar(ctx, instrumentedJar, jacocoReportClassesFile, classesJar, specs)

	j.jacocoReportClassesFile = jacocoReportClassesFile

	return instrumentedJar
}

var _ Dependency = (*Module)(nil)

func (j *Module) HeaderJars() android.Paths {
	if j.headerJarFile == nil {
		return nil
	}
	return android.Paths{j.headerJarFile}
}

func (j *Module) ImplementationJars() android.Paths {
	if j.implementationJarFile == nil {
		return nil
	}
	return android.Paths{j.implementationJarFile}
}

func (j *Module) DexJar() android.Path {
	return j.dexJarFile
}

func (j *Module) ResourceJars() android.Paths {
	if j.resourceJar == nil {
		return nil
	}
	return android.Paths{j.resourceJar}
}

func (j *Module) ImplementationAndResourcesJars() android.Paths {
	if j.implementationAndResourcesJar == nil {
		return nil
	}
	return android.Paths{j.implementationAndResourcesJar}
}

func (j *Module) AidlIncludeDirs() android.Paths {
	// exportAidlIncludeDirs is type android.Paths already
	return j.exportAidlIncludeDirs
}

func (j *Module) ExportedSdkLibs() []string {
	// exportedSdkLibs is type []string
	return j.exportedSdkLibs
}

func (j *Module) ExportedPlugins() (android.Paths, []string) {
	return j.exportedPluginJars, j.exportedPluginClasses
}

func (j *Module) SrcJarArgs() ([]string, android.Paths) {
	return j.srcJarArgs, j.srcJarDeps
}

var _ logtagsProducer = (*Module)(nil)

func (j *Module) logtags() android.Paths {
	return j.logtagsSrcs
}

// Collect information for opening IDE project files in java/jdeps.go.
func (j *Module) IDEInfo(dpInfo *android.IdeInfo) {
	dpInfo.Deps = append(dpInfo.Deps, j.CompilerDeps()...)
	dpInfo.Srcs = append(dpInfo.Srcs, j.expandIDEInfoCompiledSrcs...)
	dpInfo.SrcJars = append(dpInfo.SrcJars, j.compiledSrcJars.Strings()...)
	dpInfo.Aidl_include_dirs = append(dpInfo.Aidl_include_dirs, j.deviceProperties.Aidl.Include_dirs...)
	if j.expandJarjarRules != nil {
		dpInfo.Jarjar_rules = append(dpInfo.Jarjar_rules, j.expandJarjarRules.String())
	}
}

func (j *Module) CompilerDeps() []string {
	jdeps := []string{}
	jdeps = append(jdeps, j.properties.Libs...)
	jdeps = append(jdeps, j.properties.Static_libs...)
	return jdeps
}

func (j *Module) hasCode(ctx android.ModuleContext) bool {
	srcFiles := android.PathsForModuleSrcExcludes(ctx, j.properties.Srcs, j.properties.Exclude_srcs)
	return len(srcFiles) > 0 || len(ctx.GetDirectDepsWithTag(staticLibTag)) > 0
}

func (j *Module) DepIsInSameApex(ctx android.BaseModuleContext, dep android.Module) bool {
	depTag := ctx.OtherModuleDependencyTag(dep)
	// Dependencies other than the static linkage are all considered crossing APEX boundary
	// Also, a dependency to an sdk member is also considered as such. This is required because
	// sdk members should be mutated into APEXes. Refer to sdk.sdkDepsReplaceMutator.
	return depTag == staticLibTag || j.IsInAnySdk()
}

func (j *Module) Stem() string {
	return proptools.StringDefault(j.deviceProperties.Stem, j.Name())
}

func (j *Module) JacocoReportClassesFile() android.Path {
	return j.jacocoReportClassesFile
}

func (j *Module) IsInstallable() bool {
	return Bool(j.properties.Installable)
}

//
// Java libraries (.jar file)
//

type Library struct {
	Module

	InstallMixin func(ctx android.ModuleContext, installPath android.Path) (extraInstallDeps android.Paths)
}

func shouldUncompressDex(ctx android.ModuleContext, dexpreopter *dexpreopter) bool {
	// Store uncompressed (and aligned) any dex files from jars in APEXes.
	if am, ok := ctx.Module().(android.ApexModule); ok && !am.IsForPlatform() {
		return true
	}

	// Store uncompressed (and do not strip) dex files from boot class path jars.
	if inList(ctx.ModuleName(), ctx.Config().BootJars()) {
		return true
	}

	// Store uncompressed dex files that are preopted on /system.
	if !dexpreopter.dexpreoptDisabled(ctx) && (ctx.Host() || !odexOnSystemOther(ctx, dexpreopter.installPath)) {
		return true
	}
	if ctx.Config().UncompressPrivAppDex() &&
		inList(ctx.ModuleName(), ctx.Config().ModulesLoadedByPrivilegedModules()) {
		return true
	}

	return false
}

func (j *Library) GenerateAndroidBuildActions(ctx android.ModuleContext) {
	j.checkSdkVersion(ctx)
	j.dexpreopter.installPath = android.PathForModuleInstall(ctx, "framework", j.Stem()+".jar")
	j.dexpreopter.isSDKLibrary = j.deviceProperties.IsSDKLibrary
	j.dexpreopter.uncompressedDex = shouldUncompressDex(ctx, &j.dexpreopter)
	j.deviceProperties.UncompressDex = j.dexpreopter.uncompressedDex
	j.compile(ctx, nil)

	exclusivelyForApex := android.InAnyApex(ctx.ModuleName()) && !j.IsForPlatform()
	if (Bool(j.properties.Installable) || ctx.Host()) && !exclusivelyForApex {
		var extraInstallDeps android.Paths
		if j.InstallMixin != nil {
			extraInstallDeps = j.InstallMixin(ctx, j.outputFile)
		}
		j.installFile = ctx.InstallFile(android.PathForModuleInstall(ctx, "framework"),
			ctx.ModuleName()+".jar", j.outputFile, extraInstallDeps...)
	}
}

func (j *Library) DepsMutator(ctx android.BottomUpMutatorContext) {
	j.deps(ctx)
}

const (
	aidlIncludeDir   = "aidl"
	javaDir          = "java"
	jarFileSuffix    = ".jar"
	testConfigSuffix = "-AndroidTest.xml"
)

// path to the jar file of a java library. Relative to <sdk_root>/<api_dir>
func sdkSnapshotFilePathForJar(member android.SdkMember) string {
	return sdkSnapshotFilePathForMember(member, jarFileSuffix)
}

func sdkSnapshotFilePathForMember(member android.SdkMember, suffix string) string {
	return filepath.Join(javaDir, member.Name()+suffix)
}

type librarySdkMemberType struct {
	android.SdkMemberTypeBase

	// Function to retrieve the appropriate output jar (implementation or header) from
	// the library.
	jarToExportGetter func(j *Library) android.Path
}

func (mt *librarySdkMemberType) AddDependencies(mctx android.BottomUpMutatorContext, dependencyTag blueprint.DependencyTag, names []string) {
	mctx.AddVariationDependencies(nil, dependencyTag, names...)
}

func (mt *librarySdkMemberType) IsInstance(module android.Module) bool {
	_, ok := module.(*Library)
	return ok
}

func (mt *librarySdkMemberType) BuildSnapshot(sdkModuleContext android.ModuleContext, builder android.SnapshotBuilder, member android.SdkMember) {

	variants := member.Variants()
	if len(variants) != 1 {
		sdkModuleContext.ModuleErrorf("sdk contains %d variants of member %q but only one is allowed", len(variants), member.Name())
		for _, variant := range variants {
			sdkModuleContext.ModuleErrorf("    %q", variant)
		}
	}
	variant := variants[0]
	j := variant.(*Library)

	exportedJar := mt.jarToExportGetter(j)
	snapshotRelativeJavaLibPath := sdkSnapshotFilePathForJar(member)
	builder.CopyToSnapshot(exportedJar, snapshotRelativeJavaLibPath)

	for _, dir := range j.AidlIncludeDirs() {
		// TODO(jiyong): copy parcelable declarations only
		aidlFiles, _ := sdkModuleContext.GlobWithDeps(dir.String()+"/**/*.aidl", nil)
		for _, file := range aidlFiles {
			builder.CopyToSnapshot(android.PathForSource(sdkModuleContext, file), filepath.Join(aidlIncludeDir, file))
		}
	}

	module := builder.AddPrebuiltModule(member, "java_import")
	module.AddProperty("jars", []string{snapshotRelativeJavaLibPath})
}

var javaHeaderLibsSdkMemberType android.SdkMemberType = &librarySdkMemberType{
	android.SdkMemberTypeBase{
		PropertyName: "java_header_libs",
		SupportsSdk:  true,
	},
	func(j *Library) android.Path {
		headerJars := j.HeaderJars()
		if len(headerJars) != 1 {
			panic(fmt.Errorf("there must be only one header jar from %q", j.Name()))
		}

		return headerJars[0]
	},
}

// java_library builds and links sources into a `.jar` file for the device, and possibly for the host as well.
//
// By default, a java_library has a single variant that produces a `.jar` file containing `.class` files that were
// compiled against the device bootclasspath.  This jar is not suitable for installing on a device, but can be used
// as a `static_libs` dependency of another module.
//
// Specifying `installable: true` will product a `.jar` file containing `classes.dex` files, suitable for installing on
// a device.
//
// Specifying `host_supported: true` will produce two variants, one compiled against the device bootclasspath and one
// compiled against the host bootclasspath.
func LibraryFactory() android.Module {
	module := &Library{}

	module.AddProperties(
		&module.Module.properties,
		&module.Module.deviceProperties,
		&module.Module.dexpreoptProperties,
		&module.Module.protoProperties)

	android.InitApexModule(module)
	android.InitSdkAwareModule(module)
	InitJavaModule(module, android.HostAndDeviceSupported)
	return module
}

// java_library_static is an obsolete alias for java_library.
func LibraryStaticFactory() android.Module {
	return LibraryFactory()
}

// java_library_host builds and links sources into a `.jar` file for the host.
//
// A java_library_host has a single variant that produces a `.jar` file containing `.class` files that were
// compiled against the host bootclasspath.
func LibraryHostFactory() android.Module {
	module := &Library{}

	module.AddProperties(
		&module.Module.properties,
		&module.Module.protoProperties)

	module.Module.properties.Installable = proptools.BoolPtr(true)

	android.InitApexModule(module)
	InitJavaModule(module, android.HostSupported)
	return module
}

//
// Java Tests
//

type testProperties struct {
	// list of compatibility suites (for example "cts", "vts") that the module should be
	// installed into.
	Test_suites []string `android:"arch_variant"`

	// the name of the test configuration (for example "AndroidTest.xml") that should be
	// installed with the module.
	Test_config *string `android:"path,arch_variant"`

	// the name of the test configuration template (for example "AndroidTestTemplate.xml") that
	// should be installed with the module.
	Test_config_template *string `android:"path,arch_variant"`

	// list of files or filegroup modules that provide data that should be installed alongside
	// the test
	Data []string `android:"path"`

	// Flag to indicate whether or not to create test config automatically. If AndroidTest.xml
	// doesn't exist next to the Android.bp, this attribute doesn't need to be set to true
	// explicitly.
	Auto_gen_config *bool
}

type testHelperLibraryProperties struct {
	// list of compatibility suites (for example "cts", "vts") that the module should be
	// installed into.
	Test_suites []string `android:"arch_variant"`
}

type prebuiltTestProperties struct {
	// list of compatibility suites (for example "cts", "vts") that the module should be
	// installed into.
	Test_suites []string `android:"arch_variant"`

	// the name of the test configuration (for example "AndroidTest.xml") that should be
	// installed with the module.
	Test_config *string `android:"path,arch_variant"`
}

type Test struct {
	Library

	testProperties testProperties

	testConfig android.Path
	data       android.Paths
}

type TestHelperLibrary struct {
	Library

	testHelperLibraryProperties testHelperLibraryProperties
}

type JavaTestImport struct {
	Import

	prebuiltTestProperties prebuiltTestProperties

	testConfig android.Path
}

func (j *Test) GenerateAndroidBuildActions(ctx android.ModuleContext) {
	j.testConfig = tradefed.AutoGenJavaTestConfig(ctx, j.testProperties.Test_config, j.testProperties.Test_config_template,
		j.testProperties.Test_suites, j.testProperties.Auto_gen_config)
	j.data = android.PathsForModuleSrc(ctx, j.testProperties.Data)

	j.Library.GenerateAndroidBuildActions(ctx)
}

func (j *TestHelperLibrary) GenerateAndroidBuildActions(ctx android.ModuleContext) {
	j.Library.GenerateAndroidBuildActions(ctx)
}

func (j *JavaTestImport) GenerateAndroidBuildActions(ctx android.ModuleContext) {
	j.testConfig = tradefed.AutoGenJavaTestConfig(ctx, j.prebuiltTestProperties.Test_config, nil,
		j.prebuiltTestProperties.Test_suites, nil)

	j.Import.GenerateAndroidBuildActions(ctx)
}

type testSdkMemberType struct {
	android.SdkMemberTypeBase
}

func (mt *testSdkMemberType) AddDependencies(mctx android.BottomUpMutatorContext, dependencyTag blueprint.DependencyTag, names []string) {
	mctx.AddVariationDependencies(nil, dependencyTag, names...)
}

func (mt *testSdkMemberType) IsInstance(module android.Module) bool {
	_, ok := module.(*Test)
	return ok
}

func (mt *testSdkMemberType) BuildSnapshot(sdkModuleContext android.ModuleContext, builder android.SnapshotBuilder, member android.SdkMember) {
	variants := member.Variants()
	if len(variants) != 1 {
		sdkModuleContext.ModuleErrorf("sdk contains %d variants of member %q but only one is allowed", len(variants), member.Name())
		for _, variant := range variants {
			sdkModuleContext.ModuleErrorf("    %q", variant)
		}
	}
	variant := variants[0]
	j := variant.(*Test)

	implementationJars := j.ImplementationJars()
	if len(implementationJars) != 1 {
		panic(fmt.Errorf("there must be only one implementation jar from %q", j.Name()))
	}

	snapshotRelativeJavaLibPath := sdkSnapshotFilePathForJar(member)
	builder.CopyToSnapshot(implementationJars[0], snapshotRelativeJavaLibPath)

	snapshotRelativeTestConfigPath := sdkSnapshotFilePathForMember(member, testConfigSuffix)
	builder.CopyToSnapshot(j.testConfig, snapshotRelativeTestConfigPath)

	module := builder.AddPrebuiltModule(member, "java_test_import")
	module.AddProperty("jars", []string{snapshotRelativeJavaLibPath})
	module.AddProperty("test_config", snapshotRelativeTestConfigPath)
}

// java_test builds a and links sources into a `.jar` file for the device, and possibly for the host as well, and
// creates an `AndroidTest.xml` file to allow running the test with `atest` or a `TEST_MAPPING` file.
//
// By default, a java_test has a single variant that produces a `.jar` file containing `classes.dex` files that were
// compiled against the device bootclasspath.
//
// Specifying `host_supported: true` will produce two variants, one compiled against the device bootclasspath and one
// compiled against the host bootclasspath.
func TestFactory() android.Module {
	module := &Test{}

	module.AddProperties(
		&module.Module.properties,
		&module.Module.deviceProperties,
		&module.Module.dexpreoptProperties,
		&module.Module.protoProperties,
		&module.testProperties)

	module.Module.properties.Installable = proptools.BoolPtr(true)
	module.Module.dexpreopter.isTest = true

	InitJavaModule(module, android.HostAndDeviceSupported)
	return module
}

// java_test_helper_library creates a java library and makes sure that it is added to the appropriate test suite.
func TestHelperLibraryFactory() android.Module {
	module := &TestHelperLibrary{}

	module.AddProperties(
		&module.Module.properties,
		&module.Module.deviceProperties,
		&module.Module.dexpreoptProperties,
		&module.Module.protoProperties,
		&module.testHelperLibraryProperties)

	module.Module.properties.Installable = proptools.BoolPtr(true)
	module.Module.dexpreopter.isTest = true

	InitJavaModule(module, android.HostAndDeviceSupported)
	return module
}

// java_test_import imports one or more `.jar` files into the build graph as if they were built by a java_test module
// and makes sure that it is added to the appropriate test suite.
//
// By default, a java_test_import has a single variant that expects a `.jar` file containing `.class` files that were
// compiled against an Android classpath.
//
// Specifying `host_supported: true` will produce two variants, one for use as a dependency of device modules and one
// for host modules.
func JavaTestImportFactory() android.Module {
	module := &JavaTestImport{}

	module.AddProperties(
		&module.Import.properties,
		&module.prebuiltTestProperties)

	module.Import.properties.Installable = proptools.BoolPtr(true)

	android.InitPrebuiltModule(module, &module.properties.Jars)
	android.InitApexModule(module)
	android.InitSdkAwareModule(module)
	InitJavaModule(module, android.HostAndDeviceSupported)
	return module
}

// java_test_host builds a and links sources into a `.jar` file for the host, and creates an `AndroidTest.xml` file to
// allow running the test with `atest` or a `TEST_MAPPING` file.
//
// A java_test_host has a single variant that produces a `.jar` file containing `.class` files that were
// compiled against the host bootclasspath.
func TestHostFactory() android.Module {
	module := &Test{}

	module.AddProperties(
		&module.Module.properties,
		&module.Module.protoProperties,
		&module.testProperties)

	module.Module.properties.Installable = proptools.BoolPtr(true)

	InitJavaModule(module, android.HostSupported)
	return module
}

//
// Java Binaries (.jar file plus wrapper script)
//

type binaryProperties struct {
	// installable script to execute the resulting jar
	Wrapper *string `android:"path"`

	// Name of the class containing main to be inserted into the manifest as Main-Class.
	Main_class *string
}

type Binary struct {
	Library

	binaryProperties binaryProperties

	isWrapperVariant bool

	wrapperFile android.Path
	binaryFile  android.InstallPath
}

func (j *Binary) HostToolPath() android.OptionalPath {
	return android.OptionalPathForPath(j.binaryFile)
}

func (j *Binary) GenerateAndroidBuildActions(ctx android.ModuleContext) {
	if ctx.Arch().ArchType == android.Common {
		// Compile the jar
		if j.binaryProperties.Main_class != nil {
			if j.properties.Manifest != nil {
				ctx.PropertyErrorf("main_class", "main_class cannot be used when manifest is set")
			}
			manifestFile := android.PathForModuleOut(ctx, "manifest.txt")
			GenerateMainClassManifest(ctx, manifestFile, String(j.binaryProperties.Main_class))
			j.overrideManifest = android.OptionalPathForPath(manifestFile)
		}

		j.Library.GenerateAndroidBuildActions(ctx)
	} else {
		// Handle the binary wrapper
		j.isWrapperVariant = true

		if j.binaryProperties.Wrapper != nil {
			j.wrapperFile = android.PathForModuleSrc(ctx, *j.binaryProperties.Wrapper)
		} else {
			j.wrapperFile = android.PathForSource(ctx, "build/soong/scripts/jar-wrapper.sh")
		}

		// Depend on the installed jar so that the wrapper doesn't get executed by
		// another build rule before the jar has been installed.
		jarFile := ctx.PrimaryModule().(*Binary).installFile

		j.binaryFile = ctx.InstallExecutable(android.PathForModuleInstall(ctx, "bin"),
			ctx.ModuleName(), j.wrapperFile, jarFile)
	}
}

func (j *Binary) DepsMutator(ctx android.BottomUpMutatorContext) {
	if ctx.Arch().ArchType == android.Common {
		j.deps(ctx)
	}
}

// java_binary builds a `.jar` file and a shell script that executes it for the device, and possibly for the host
// as well.
//
// By default, a java_binary has a single variant that produces a `.jar` file containing `classes.dex` files that were
// compiled against the device bootclasspath.
//
// Specifying `host_supported: true` will produce two variants, one compiled against the device bootclasspath and one
// compiled against the host bootclasspath.
func BinaryFactory() android.Module {
	module := &Binary{}

	module.AddProperties(
		&module.Module.properties,
		&module.Module.deviceProperties,
		&module.Module.dexpreoptProperties,
		&module.Module.protoProperties,
		&module.binaryProperties)

	module.Module.properties.Installable = proptools.BoolPtr(true)

	android.InitAndroidArchModule(module, android.HostAndDeviceSupported, android.MultilibCommonFirst)
	android.InitDefaultableModule(module)
	return module
}

// java_binary_host builds a `.jar` file and a shell script that executes it for the host.
//
// A java_binary_host has a single variant that produces a `.jar` file containing `.class` files that were
// compiled against the host bootclasspath.
func BinaryHostFactory() android.Module {
	module := &Binary{}

	module.AddProperties(
		&module.Module.properties,
		&module.Module.protoProperties,
		&module.binaryProperties)

	module.Module.properties.Installable = proptools.BoolPtr(true)

	android.InitAndroidArchModule(module, android.HostSupported, android.MultilibCommonFirst)
	android.InitDefaultableModule(module)
	return module
}

//
// Java prebuilts
//

type ImportProperties struct {
	Jars []string `android:"path"`

	Sdk_version *string

	Installable *bool

	// List of shared java libs that this module has dependencies to
	Libs []string

	// List of files to remove from the jar file(s)
	Exclude_files []string

	// List of directories to remove from the jar file(s)
	Exclude_dirs []string

	// if set to true, run Jetifier against .jar file. Defaults to false.
	Jetifier *bool

	// set the name of the output
	Stem *string
}

type Import struct {
	android.ModuleBase
	android.DefaultableModuleBase
	android.ApexModuleBase
	prebuilt android.Prebuilt
	android.SdkBase

	properties ImportProperties

	combinedClasspathFile android.Path
	exportedSdkLibs       []string
}

func (j *Import) sdkVersion() sdkSpec {
	return sdkSpecFrom(String(j.properties.Sdk_version))
}

func (j *Import) minSdkVersion() sdkSpec {
	return j.sdkVersion()
}

func (j *Import) Prebuilt() *android.Prebuilt {
	return &j.prebuilt
}

func (j *Import) PrebuiltSrcs() []string {
	return j.properties.Jars
}

func (j *Import) Name() string {
	return j.prebuilt.Name(j.ModuleBase.Name())
}

func (j *Import) Stem() string {
	return proptools.StringDefault(j.properties.Stem, j.ModuleBase.Name())
}

func (a *Import) JacocoReportClassesFile() android.Path {
	return nil
}

func (j *Import) DepsMutator(ctx android.BottomUpMutatorContext) {
	ctx.AddVariationDependencies(nil, libTag, j.properties.Libs...)
}

func (j *Import) GenerateAndroidBuildActions(ctx android.ModuleContext) {
	jars := android.PathsForModuleSrc(ctx, j.properties.Jars)

	jarName := j.Stem() + ".jar"
	outputFile := android.PathForModuleOut(ctx, "combined", jarName)
	TransformJarsToJar(ctx, outputFile, "for prebuilts", jars, android.OptionalPath{},
		false, j.properties.Exclude_files, j.properties.Exclude_dirs)
	if Bool(j.properties.Jetifier) {
		inputFile := outputFile
		outputFile = android.PathForModuleOut(ctx, "jetifier", jarName)
		TransformJetifier(ctx, outputFile, inputFile)
	}
	j.combinedClasspathFile = outputFile

	ctx.VisitDirectDeps(func(module android.Module) {
		otherName := ctx.OtherModuleName(module)
		tag := ctx.OtherModuleDependencyTag(module)

		switch dep := module.(type) {
		case Dependency:
			switch tag {
			case libTag, staticLibTag:
				// sdk lib names from dependencies are re-exported
				j.exportedSdkLibs = append(j.exportedSdkLibs, dep.ExportedSdkLibs()...)
			}
		case SdkLibraryDependency:
			switch tag {
			case libTag:
				// names of sdk libs that are directly depended are exported
				j.exportedSdkLibs = append(j.exportedSdkLibs, otherName)
			}
		}
	})

	j.exportedSdkLibs = android.FirstUniqueStrings(j.exportedSdkLibs)
	if Bool(j.properties.Installable) {
		ctx.InstallFile(android.PathForModuleInstall(ctx, "framework"),
			jarName, outputFile)
	}
}

var _ Dependency = (*Import)(nil)

func (j *Import) HeaderJars() android.Paths {
	if j.combinedClasspathFile == nil {
		return nil
	}
	return android.Paths{j.combinedClasspathFile}
}

func (j *Import) ImplementationJars() android.Paths {
	if j.combinedClasspathFile == nil {
		return nil
	}
	return android.Paths{j.combinedClasspathFile}
}

func (j *Import) ResourceJars() android.Paths {
	return nil
}

func (j *Import) ImplementationAndResourcesJars() android.Paths {
	if j.combinedClasspathFile == nil {
		return nil
	}
	return android.Paths{j.combinedClasspathFile}
}

func (j *Import) DexJar() android.Path {
	return nil
}

func (j *Import) AidlIncludeDirs() android.Paths {
	return nil
}

func (j *Import) ExportedSdkLibs() []string {
	return j.exportedSdkLibs
}

func (j *Import) ExportedPlugins() (android.Paths, []string) {
	return nil, nil
}

func (j *Import) SrcJarArgs() ([]string, android.Paths) {
	return nil, nil
}

func (j *Import) DepIsInSameApex(ctx android.BaseModuleContext, dep android.Module) bool {
	depTag := ctx.OtherModuleDependencyTag(dep)
	// dependencies other than the static linkage are all considered crossing APEX boundary
	// Also, a dependency to an sdk member is also considered as such. This is required because
	// sdk members should be mutated into APEXes. Refer to sdk.sdkDepsReplaceMutator.
	return depTag == staticLibTag || j.IsInAnySdk()
}

// Add compile time check for interface implementation
var _ android.IDEInfo = (*Import)(nil)
var _ android.IDECustomizedModuleName = (*Import)(nil)

// Collect information for opening IDE project files in java/jdeps.go.
const (
	removedPrefix = "prebuilt_"
)

func (j *Import) IDEInfo(dpInfo *android.IdeInfo) {
	dpInfo.Jars = append(dpInfo.Jars, j.PrebuiltSrcs()...)
}

func (j *Import) IDECustomizedModuleName() string {
	// TODO(b/113562217): Extract the base module name from the Import name, often the Import name
	// has a prefix "prebuilt_". Remove the prefix explicitly if needed until we find a better
	// solution to get the Import name.
	name := j.Name()
	if strings.HasPrefix(name, removedPrefix) {
		name = strings.TrimPrefix(name, removedPrefix)
	}
	return name
}

var _ android.PrebuiltInterface = (*Import)(nil)

// java_import imports one or more `.jar` files into the build graph as if they were built by a java_library module.
//
// By default, a java_import has a single variant that expects a `.jar` file containing `.class` files that were
// compiled against an Android classpath.
//
// Specifying `host_supported: true` will produce two variants, one for use as a dependency of device modules and one
// for host modules.
func ImportFactory() android.Module {
	module := &Import{}

	module.AddProperties(&module.properties)

	android.InitPrebuiltModule(module, &module.properties.Jars)
	android.InitApexModule(module)
	android.InitSdkAwareModule(module)
	InitJavaModule(module, android.HostAndDeviceSupported)
	return module
}

// java_import imports one or more `.jar` files into the build graph as if they were built by a java_library_host
// module.
//
// A java_import_host has a single variant that expects a `.jar` file containing `.class` files that were
// compiled against a host bootclasspath.
func ImportFactoryHost() android.Module {
	module := &Import{}

	module.AddProperties(&module.properties)

	android.InitPrebuiltModule(module, &module.properties.Jars)
	android.InitApexModule(module)
	InitJavaModule(module, android.HostSupported)
	return module
}

// dex_import module

type DexImportProperties struct {
	Jars []string `android:"path"`

	// set the name of the output
	Stem *string
}

type DexImport struct {
	android.ModuleBase
	android.DefaultableModuleBase
	android.ApexModuleBase
	prebuilt android.Prebuilt

	properties DexImportProperties

	dexJarFile              android.Path
	maybeStrippedDexJarFile android.Path

	dexpreopter
}

func (j *DexImport) Prebuilt() *android.Prebuilt {
	return &j.prebuilt
}

func (j *DexImport) PrebuiltSrcs() []string {
	return j.properties.Jars
}

func (j *DexImport) Name() string {
	return j.prebuilt.Name(j.ModuleBase.Name())
}

func (j *DexImport) Stem() string {
	return proptools.StringDefault(j.properties.Stem, j.ModuleBase.Name())
}

func (j *DexImport) IsInstallable() bool {
	return true
}

func (j *DexImport) GenerateAndroidBuildActions(ctx android.ModuleContext) {
	if len(j.properties.Jars) != 1 {
		ctx.PropertyErrorf("jars", "exactly one jar must be provided")
	}

	j.dexpreopter.installPath = android.PathForModuleInstall(ctx, "framework", j.Stem()+".jar")
	j.dexpreopter.uncompressedDex = shouldUncompressDex(ctx, &j.dexpreopter)

	inputJar := ctx.ExpandSource(j.properties.Jars[0], "jars")
	dexOutputFile := android.PathForModuleOut(ctx, ctx.ModuleName()+".jar")

	if j.dexpreopter.uncompressedDex {
		rule := android.NewRuleBuilder()

		temporary := android.PathForModuleOut(ctx, ctx.ModuleName()+".jar.unaligned")
		rule.Temporary(temporary)

		// use zip2zip to uncompress classes*.dex files
		rule.Command().
			BuiltTool(ctx, "zip2zip").
			FlagWithInput("-i ", inputJar).
			FlagWithOutput("-o ", temporary).
			FlagWithArg("-0 ", "'classes*.dex'")

		// use zipalign to align uncompressed classes*.dex files
		rule.Command().
			BuiltTool(ctx, "zipalign").
			Flag("-f").
			Text("4").
			Input(temporary).
			Output(dexOutputFile)

		rule.DeleteTemporaryFiles()

		rule.Build(pctx, ctx, "uncompress_dex", "uncompress dex")
	} else {
		ctx.Build(pctx, android.BuildParams{
			Rule:   android.Cp,
			Input:  inputJar,
			Output: dexOutputFile,
		})
	}

	j.dexJarFile = dexOutputFile

	dexOutputFile = j.dexpreopt(ctx, dexOutputFile)

	j.maybeStrippedDexJarFile = dexOutputFile

	ctx.InstallFile(android.PathForModuleInstall(ctx, "framework"),
		ctx.ModuleName()+".jar", dexOutputFile)
}

func (j *DexImport) DexJar() android.Path {
	return j.dexJarFile
}

// dex_import imports a `.jar` file containing classes.dex files.
//
// A dex_import module cannot be used as a dependency of a java_* or android_* module, it can only be installed
// to the device.
func DexImportFactory() android.Module {
	module := &DexImport{}

	module.AddProperties(&module.properties)

	android.InitPrebuiltModule(module, &module.properties.Jars)
	android.InitApexModule(module)
	InitJavaModule(module, android.DeviceSupported)
	return module
}

//
// Defaults
//
type Defaults struct {
	android.ModuleBase
	android.DefaultsModuleBase
	android.ApexModuleBase
}

// java_defaults provides a set of properties that can be inherited by other java or android modules.
//
// A module can use the properties from a java_defaults module using `defaults: ["defaults_module_name"]`.  Each
// property in the defaults module that exists in the depending module will be prepended to the depending module's
// value for that property.
//
// Example:
//
//     java_defaults {
//         name: "example_defaults",
//         srcs: ["common/**/*.java"],
//         javacflags: ["-Xlint:all"],
//         aaptflags: ["--auto-add-overlay"],
//     }
//
//     java_library {
//         name: "example",
//         defaults: ["example_defaults"],
//         srcs: ["example/**/*.java"],
//     }
//
// is functionally identical to:
//
//     java_library {
//         name: "example",
//         srcs: [
//             "common/**/*.java",
//             "example/**/*.java",
//         ],
//         javacflags: ["-Xlint:all"],
//     }
func defaultsFactory() android.Module {
	return DefaultsFactory()
}

func DefaultsFactory() android.Module {
	module := &Defaults{}

	module.AddProperties(
		&CompilerProperties{},
		&CompilerDeviceProperties{},
		&DexpreoptProperties{},
		&android.ProtoProperties{},
		&aaptProperties{},
		&androidLibraryProperties{},
		&appProperties{},
		&appTestProperties{},
		&overridableAppProperties{},
		&ImportProperties{},
		&AARImportProperties{},
		&sdkLibraryProperties{},
		&DexImportProperties{},
		&android.ApexProperties{},
	)

	android.InitDefaultsModule(module)
	return module
}

func kytheExtractJavaFactory() android.Singleton {
	return &kytheExtractJavaSingleton{}
}

type kytheExtractJavaSingleton struct {
}

func (ks *kytheExtractJavaSingleton) GenerateBuildActions(ctx android.SingletonContext) {
	var xrefTargets android.Paths
	ctx.VisitAllModules(func(module android.Module) {
		if javaModule, ok := module.(xref); ok {
			xrefTargets = append(xrefTargets, javaModule.XrefJavaFiles()...)
		}
	})
	// TODO(asmundak): perhaps emit a rule to output a warning if there were no xrefTargets
	if len(xrefTargets) > 0 {
		ctx.Build(pctx, android.BuildParams{
			Rule:   blueprint.Phony,
			Output: android.PathForPhony(ctx, "xref_java"),
			Inputs: xrefTargets,
		})
	}
}

var Bool = proptools.Bool
var BoolDefault = proptools.BoolDefault
var String = proptools.String
var inList = android.InList
