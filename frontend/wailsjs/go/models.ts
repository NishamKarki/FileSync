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

