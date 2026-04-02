package policy
import rego.v1

default allow = false
default hardware_failing = false

allow if {
    count(input.submods) > 0
    not executable_failing
    not configuration_failing
    not hardware_failing
}

executable_failing if {
    some _, submod in input.submods
    executables := submod["ear.trustworthiness-vector"]["executables"]
    not in_affirming_range(executables)
}

configuration_failing if {
    some _, submod in input.submods
    configuration := submod["ear.trustworthiness-vector"]["configuration"]
    not in_affirming_range(configuration)
}

# Hardware trust claims are enforced by default. For TDX, no additional
# RVPS values are needed. For SNP, you must provide hardware-specific
# RVPS values (tcb_bootloader, tcb_microcode, tcb_snp, tcb_tee) from
# your environment. If these values are not available, comment out
# the following block.
hardware_failing if {
   some _, submod in input.submods
   hardware := submod["ear.trustworthiness-vector"]["hardware"]
   not in_affirming_range(hardware)
}

in_affirming_range(val) if {
    val >= 2
    val <= 31
}
