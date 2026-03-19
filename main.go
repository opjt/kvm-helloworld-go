package main

import (
	"log"
	"os"
	"syscall"
	"unsafe"
)

// ioctl 번호: _IO(KVMIO, nr) = (0xAE << 8) | nr
// https://elixir.bootlin.com/linux/v6.19.8/source/include/uapi/asm-generic/ioctl.h
const (
	KVM_CREATE_VM    = 0xAE01
	KVM_CREATE_VCPU  = 0xAE41
	KVM_RUN          = 0xAE80
	KVM_SET_TSS_ADDR = 0xAE47 // KVM 내부 TSS 주소 설정 (필수)

	KVM_GET_VCPU_MMAP_SIZE     = 0xAE04     // kvm_run 구조체 mmap 크기 조회
	KVM_SET_USER_MEMORY_REGION = 0x4020AE46 // Register Guest Physical Memory

	KVM_GET_REGS  = 0x8090ae81 // 일반 레지스터 읽기
	KVM_SET_REGS  = 0x4090ae82 // 일반 레지스터 쓰기
	KVM_GET_SREGS = 0x8138ae83 // 특수 레지스터 읽기
	KVM_SET_SREGS = 0x4138ae84 // 특수 레지스터 쓰기

	KVM_EXIT_UNKNOWN = 0 // 알 수 없는 VM-Exit
	KVM_EXIT_HLT     = 5 // 게스트가 HLT 명령어 실행
)

// 게스트 물리메모리 슬롯 등록 구조체
// https://elixir.bootlin.com/linux/latest/source/include/uapi/linux/kvm.h
type KvmUserspaceMemoryRegion struct {
	Slot          uint32
	Flags         uint32
	GuestPhysAddr uint64
	MemorySize    uint64
	UserspaceAddr uint64 // 호스트 프로세스의 mmap 주소
}

// KVM_RUN 후 커널이 채워주는 VM-Exit 정보
// vcpu_fd를 mmap해서 공유메모리로 읽음
type KvmRun struct {
	RequestInterruptWindow uint8
	ImmediateExit          uint8
	Padding1               [6]uint8
	ExitReason             uint32 // KVM_EXIT_HLT, KVM_EXIT_IO 등
}

// x86_64 일반 레지스터
type KvmRegs struct {
	Rax, Rbx, Rcx, Rdx uint64
	Rsi, Rdi, Rsp, Rbp uint64
	R8, R9, R10, R11   uint64
	R12, R13, R14, R15 uint64
	Rip, Rflags        uint64
}

// x86 세그먼트 디스크립터
type KvmSegment struct {
	Base     uint64
	Limit    uint32
	Selector uint16
	Type     uint8
	Present  uint8
	Dpl      uint8
	Db       uint8
	S        uint8
	L        uint8
	G        uint8
	Avl      uint8
	Unusable uint8
	Padding  uint8
}

// GDT/IDT 디스크립터 테이블
type KvmDtable struct {
	Base    uint64
	Limit   uint16
	Padding [3]uint16
}

// x86 특수 레지스터 (세그먼트, 컨트롤 레지스터)
type KvmSregs struct {
	Cs, Ds, Es, Fs, Gs, Ss  KvmSegment
	Tr, Ldt                 KvmSegment
	Gdt, Idt                KvmDtable
	Cr0, Cr2, Cr3, Cr4, Cr8 uint64
	Efer                    uint64
	ApicBase                uint64
	InterruptBitmap         [4]uint64
}

func main() {
	// 1. open /dev/kvm
	kvmFile, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		log.Fatal(err)
	}
	defer kvmFile.Close()
	kvmFd := kvmFile.Fd()

	// 2. Create vm
	vmFd, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		kvmFd,
		uintptr(KVM_CREATE_VM),
		uintptr(0),
	)
	if errno != 0 {
		log.Fatal("KVM_CREATE_VM failed:", errno)
	}

	// 3. Allocate 1MB of host memory for guest physical memory
	mem, err := syscall.Mmap(
		-1, 0, 1<<20,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_SHARED|syscall.MAP_ANON,
	)
	if err != nil {
		log.Fatal(err)
	}
	defer syscall.Munmap(mem)

	// 4. Write HLT (0xF4) to address 0
	// HLT : https://en.wikipedia.org/wiki/HLT_(x86_instruction)
	// vCPU starts at CS=0, RIP=0 -> execute HLT -> VM-Exit
	mem[0] = 0xF4

	// 5. Register guest physical memory with KVM
	region := KvmUserspaceMemoryRegion{
		Slot:          0,
		Flags:         0, // (read/write)
		GuestPhysAddr: 0,
		MemorySize:    1 << 20,
		UserspaceAddr: uint64(uintptr(unsafe.Pointer(&mem[0]))),
	}
	_, _, errno = syscall.Syscall(
		syscall.SYS_IOCTL,
		vmFd,
		uintptr(KVM_SET_USER_MEMORY_REGION),
		uintptr(unsafe.Pointer(&region)),
	)
	if errno != 0 {
		log.Fatal("KVM_SET_USER_MEMORY_REGION failed:", errno)
	}

	// 6. set TSS address
	_, _, errno = syscall.Syscall(
		syscall.SYS_IOCTL,
		vmFd,
		uintptr(KVM_SET_TSS_ADDR),
		uintptr(0xfffbd000), // outside the guest memory
	)
	if errno != 0 {
		log.Fatal("KVM_SET_TSS_ADDR failed:", errno)
	}

	// 7. create vCPU
	vcpuFd, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		vmFd,
		uintptr(KVM_CREATE_VCPU),
		uintptr(0),
	)
	if errno != 0 {
		log.Fatal("KVM_CREATE_VCPU failed:", errno)
	}

	// 8. Get kvm_run struct size
	// after KVM_RUN, the kernel writes VM-Exit info to this shared memory
	mmapSize, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		kvmFd,
		uintptr(KVM_GET_VCPU_MMAP_SIZE),
		uintptr(0),
	)
	if errno != 0 {
		log.Fatal("KVM_GET_VCPU_MMAP_SIZE failed:", errno)
	}
	kvmRunMmap, err := syscall.Mmap(
		int(vcpuFd),
		0,
		int(mmapSize),
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_SHARED,
	)
	if err != nil {
		log.Fatal(err)
	}
	defer syscall.Munmap(kvmRunMmap)

	kvmRun := (*KvmRun)(unsafe.Pointer(&kvmRunMmap[0]))

	// 9. Set special registers for real mode
	// CS.base=0, CS.selector=0, clear CR0.bit -> real mode (16-bit)
	var sregs KvmSregs // special registers
	_, _, errno = syscall.Syscall(
		syscall.SYS_IOCTL,
		vcpuFd,
		uintptr(KVM_GET_SREGS),
		uintptr(unsafe.Pointer(&sregs)),
	)
	if errno != 0 {
		log.Fatal("KVM_GET_SREGS failed:", errno)
	}
	sregs.Cs.Base = 0
	sregs.Cs.Selector = 0
	sregs.Cs.Limit = 0xFFFF            // 64KB
	sregs.Cs.Present = 1               // valid segment
	sregs.Cs.Type = 0xB                // Execute/Read, accessed (cpu manual Code-and Data-Segment Descriptor Types)
	sregs.Cs.S = 1                     // code/data segment
	sregs.Cr0 = sregs.Cr0 &^ uint64(1) // PE bit off (real mode)
	_, _, errno = syscall.Syscall(
		syscall.SYS_IOCTL,
		vcpuFd,
		uintptr(KVM_SET_SREGS),
		uintptr(unsafe.Pointer(&sregs)),
	)
	if errno != 0 {
		log.Fatal("KVM_SET_SREGS failed:", errno)
	}

	// 10. Set general purpose registers
	var regs KvmRegs
	_, _, errno = syscall.Syscall(
		syscall.SYS_IOCTL,
		vcpuFd,
		uintptr(KVM_GET_REGS),
		uintptr(unsafe.Pointer(&regs)),
	)
	if errno != 0 {
		log.Fatal("KVM_GET_REGS failed:", errno)
	}
	regs.Rip = 0      // cs:ip
	regs.Rflags = 0x2 // Reserved, always 1 (x86 spec)
	_, _, errno = syscall.Syscall(
		syscall.SYS_IOCTL,
		vcpuFd,
		uintptr(KVM_SET_REGS),
		uintptr(unsafe.Pointer(&regs)),
	)
	if errno != 0 {
		log.Fatal("KVM_SET_REGS failed:", errno)
	}

	// 11. run vCPU
	// VM-Exit 발생하면 ioctl 반환, kvmRun.ExitReason으로 원인 확인
	_, _, errno = syscall.Syscall(
		syscall.SYS_IOCTL,
		vcpuFd,
		uintptr(KVM_RUN),
		uintptr(0),
	)
	if errno != 0 {
		log.Fatal("KVM_RUN failed:", errno)
	}

	switch kvmRun.ExitReason {
	case KVM_EXIT_HLT:
		log.Println("VM halted!")
	default:
		log.Printf("unexpected exit: %d\n", kvmRun.ExitReason)
	}
}
