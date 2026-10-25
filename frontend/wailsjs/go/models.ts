export namespace fsnotify {

	export class Event {
	    Name: string;
	    Op: number;

	    static createFrom(source: any = {}) {
	        return new Event(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Name = source["Name"];
	        this.Op = source["Op"];
	    }
	}

}

export namespace main {

	export class Chunk {
	    Index: number;
	    Data: number[];
	    Hash: string;

	    static createFrom(source: any = {}) {
	        return new Chunk(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Index = source["Index"];
	        this.Data = source["Data"];
	        this.Hash = source["Hash"];
	    }
	}
	export class FileImportResult {
	    names: string[];
	    errors: string[];

	    static createFrom(source: any = {}) {
	        return new FileImportResult(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.names = source["names"];
	        this.errors = source["errors"];
	    }
	}
	export class SyncResult {
	    keepLocal?: boolean;
	    baseUpdated: boolean;
	    totalChunks: number;
	    chunksSent: number;
	    chunkBytesSent: number;
	    conflictFile?: string;

	    static createFrom(source: any = {}) {
	        return new SyncResult(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.keepLocal = source["keepLocal"];
	        this.baseUpdated = source["baseUpdated"];
	        this.totalChunks = source["totalChunks"];
	        this.chunksSent = source["chunksSent"];
	        this.chunkBytesSent = source["chunkBytesSent"];
	        this.conflictFile = source["conflictFile"];
	    }
	}
	export class SyncedFile {
	    name: string;
	    size: number;
	    modified: string;

	    static createFrom(source: any = {}) {
	        return new SyncedFile(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.size = source["size"];
	        this.modified = source["modified"];
	    }
	}

}

export namespace network {

	export class Device {
	    id: string;
	    name: string;
	    ip: string;
	    port: number;
	    online: boolean;
	    lastSeen: string;
	    coordinator: boolean;

	    static createFrom(source: any = {}) {
	        return new Device(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.ip = source["ip"];
	        this.port = source["port"];
	        this.online = source["online"];
	        this.lastSeen = source["lastSeen"];
	        this.coordinator = source["coordinator"];
	    }
	}
	export class PingResponse {
	    success: boolean;
	    message: string;
	    deviceName: string;
	    deviceId: string;
	    coordinator: boolean;

	    static createFrom(source: any = {}) {
	        return new PingResponse(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.message = source["message"];
	        this.deviceName = source["deviceName"];
	        this.deviceId = source["deviceId"];
	        this.coordinator = source["coordinator"];
	    }
	}
	export class RegistrationStatus {
	    deviceId: string;
	    deviceName: string;
	    localAddress: string;
	    coordinator: boolean;
	    coordinatorAddress: string;
	    configured: boolean;
	    registered: boolean;
	    lastError: string;

	    static createFrom(source: any = {}) {
	        return new RegistrationStatus(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.deviceId = source["deviceId"];
	        this.deviceName = source["deviceName"];
	        this.localAddress = source["localAddress"];
	        this.coordinator = source["coordinator"];
	        this.coordinatorAddress = source["coordinatorAddress"];
	        this.configured = source["configured"];
	        this.registered = source["registered"];
	        this.lastError = source["lastError"];
	    }
	}

}

