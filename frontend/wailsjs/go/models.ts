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

export namespace network {
	
	export class Device {
	    id: string;
	    name: string;
	    ip: string;
	    port: number;
	    online: boolean;
	
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
	    }
	}
	export class PingResponse {
	    success: boolean;
	    message: string;
	    deviceName: string;
	
	    static createFrom(source: any = {}) {
	        return new PingResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.message = source["message"];
	        this.deviceName = source["deviceName"];
	    }
	}

}

